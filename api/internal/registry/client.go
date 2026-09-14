package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Flavors. A registry's tag listing is the same everywhere, but listing what repositories
// exist is not standardised in any useful way, so each one that differs gets a name.
const (
	FlavorGenericV2 = "generic-v2"
	FlavorHarbor    = "harbor"
	FlavorDockerHub = "dockerhub"
	FlavorGHCR      = "ghcr"
)

// Credentials identify and authenticate to one registry.
type Credentials struct {
	Registry string // as stored on the secret; normalised on use
	Username string
	Password string
	Flavor   string // empty means detect from the host
}

// DetectFlavor guesses a registry's flavor from its host.
//
// A guess, not a certainty -- Harbor answers on any hostname -- so the credential carries an
// explicit flavor that overrides this. The guess only has to be right often enough that
// most users never set it.
func DetectFlavor(registry string) string {
	key, _ := NormalizeRegistry(registry)
	switch {
	case key == DockerHubAuthsKey:
		return FlavorDockerHub
	case strings.HasPrefix(key, "ghcr.io"):
		return FlavorGHCR
	case strings.Contains(key, "harbor"):
		return FlavorHarbor
	default:
		return FlavorGenericV2
	}
}

func (c Credentials) flavor() string {
	if c.Flavor != "" {
		return c.Flavor
	}
	return DetectFlavor(c.Registry)
}

// Client talks to container registries.
//
// Hand-rolled rather than built on an OCI library: this needs three operations, neither Go
// module here carries a registry dependency today, and the flavor adapters below fall
// outside what such a library would cover anyway.
type Client struct {
	http  *http.Client
	mu    sync.Mutex
	cache map[string]cacheEntry
}

type cacheEntry struct {
	value   []string
	expires time.Time
}

// Cache lifetimes are short. The UI calls these on every form render, and the cost of a
// stale entry is a tag that appeared seconds ago not being offered yet.
const (
	catalogTTL = 60 * time.Second
	tagsTTL    = 30 * time.Second
	// A registry that will not answer promptly should fail the form, not hang it.
	requestTimeout = 15 * time.Second
)

func NewClient() *Client {
	return &Client{
		http:  &http.Client{Timeout: requestTimeout},
		cache: map[string]cacheEntry{},
	}
}

func (c *Client) cached(key string) ([]string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.cache[key]
	if !ok || time.Now().After(e.expires) {
		return nil, false
	}
	return e.value, true
}

func (c *Client) store(key string, value []string, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cache[key] = cacheEntry{value: value, expires: time.Now().Add(ttl)}
}

// Ping checks that the registry is reachable and the credentials are accepted.
//
// It separates the two failures deliberately: "cannot reach this host" and "this host
// rejected these credentials" send you to different places, and a single "connection
// failed" would not.
func (c *Client) Ping(ctx context.Context, creds Credentials) error {
	_, base := NormalizeRegistry(creds.Registry)

	resp, err := c.doAuthed(ctx, creds, base+"/v2/")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))

	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("the registry rejected these credentials")
	case resp.StatusCode >= 500:
		return fmt.Errorf("the registry returned an error (HTTP %d)", resp.StatusCode)
	case resp.StatusCode >= 400 && resp.StatusCode != http.StatusNotFound:
		// 404 on /v2/ is tolerated: some registries only expose the versioned endpoints.
		return fmt.Errorf("unexpected response from the registry (HTTP %d)", resp.StatusCode)
	}
	return nil
}

// ListRepositories returns the repositories a credential can see.
func (c *Client) ListRepositories(ctx context.Context, creds Credentials) ([]string, error) {
	key, _ := NormalizeRegistry(creds.Registry)
	cacheKey := "repos:" + key + ":" + creds.Username
	if v, ok := c.cached(cacheKey); ok {
		return v, nil
	}

	var (
		repos []string
		err   error
	)
	switch creds.flavor() {
	case FlavorDockerHub:
		repos, err = c.dockerHubRepositories(ctx, creds)
	case FlavorHarbor:
		repos, err = c.harborRepositories(ctx, creds)
	default:
		repos, err = c.catalogRepositories(ctx, creds)
	}
	if err != nil {
		return nil, err
	}

	c.store(cacheKey, repos, catalogTTL)
	return repos, nil
}

// ListTags returns the tags of one repository. Every flavor answers this the same way.
func (c *Client) ListTags(ctx context.Context, creds Credentials, repository string) ([]string, error) {
	repository = strings.Trim(repository, "/")
	if repository == "" {
		return nil, fmt.Errorf("no repository given")
	}

	key, _ := NormalizeRegistry(creds.Registry)
	cacheKey := "tags:" + key + ":" + repository
	if v, ok := c.cached(cacheKey); ok {
		return v, nil
	}

	_, base := NormalizeRegistry(creds.Registry)
	var out struct {
		Tags []string `json:"tags"`
	}
	if err := c.getJSON(ctx, creds, base+"/v2/"+repository+"/tags/list", &out); err != nil {
		return nil, err
	}

	c.store(cacheKey, out.Tags, tagsTTL)
	return out.Tags, nil
}

// catalogRepositories uses the standard v2 catalog.
func (c *Client) catalogRepositories(ctx context.Context, creds Credentials) ([]string, error) {
	_, base := NormalizeRegistry(creds.Registry)
	var out struct {
		Repositories []string `json:"repositories"`
	}
	if err := c.getJSON(ctx, creds, base+"/v2/_catalog?n=1000", &out); err != nil {
		return nil, err
	}
	return out.Repositories, nil
}

// harborRepositories walks Harbor's project API.
//
// Harbor does implement /v2/_catalog, but it returns only what the credential can read and
// is disabled on some deployments; the project API is the supported path and is what the
// Harbor UI itself uses.
func (c *Client) harborRepositories(ctx context.Context, creds Credentials) ([]string, error) {
	_, base := NormalizeRegistry(creds.Registry)

	var projects []struct {
		Name string `json:"name"`
	}
	if err := c.getJSON(ctx, creds, base+"/api/v2.0/projects?page_size=100", &projects); err != nil {
		// A Harbor that will not answer its own API is still a v2 registry.
		return c.catalogRepositories(ctx, creds)
	}

	var repos []string
	for _, p := range projects {
		var items []struct {
			Name string `json:"name"`
		}
		endpoint := fmt.Sprintf("%s/api/v2.0/projects/%s/repositories?page_size=100",
			base, url.PathEscape(p.Name))
		if err := c.getJSON(ctx, creds, endpoint, &items); err != nil {
			continue // one unreadable project should not empty the list
		}
		for _, it := range items {
			repos = append(repos, it.Name)
		}
	}
	return repos, nil
}

// dockerHubRepositories uses Docker Hub's own API.
//
// Docker Hub's /v2/_catalog is not usable -- it is not implemented for the public registry --
// so there is no generic path here. Repositories are listed per namespace, which means the
// credential's username is the namespace unless it owns organisations.
func (c *Client) dockerHubRepositories(ctx context.Context, creds Credentials) ([]string, error) {
	if creds.Username == "" {
		return nil, fmt.Errorf("Docker Hub needs a username to list repositories")
	}

	var out struct {
		Results []struct {
			Name string `json:"name"`
		} `json:"results"`
	}
	endpoint := fmt.Sprintf("https://hub.docker.com/v2/repositories/%s/?page_size=100",
		url.PathEscape(creds.Username))
	if err := c.getJSON(ctx, creds, endpoint, &out); err != nil {
		return nil, err
	}

	repos := make([]string, 0, len(out.Results))
	for _, r := range out.Results {
		repos = append(repos, creds.Username+"/"+r.Name)
	}
	return repos, nil
}

func (c *Client) getJSON(ctx context.Context, creds Credentials, url string, into interface{}) error {
	resp, err := c.doAuthed(ctx, creds, url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("registry returned HTTP %d", resp.StatusCode)
	}
	return json.Unmarshal(body, into)
}

// doAuthed performs the Docker Registry v2 authentication dance.
//
// A registry answers an unauthenticated request with 401 and a WWW-Authenticate header
// naming a token service and the scope being asked for. The client fetches a bearer token
// from that service using basic auth, then repeats the original request with it. Registries
// that accept basic auth directly skip the middle step, which is why basic is tried first.
func (c *Client) doAuthed(ctx context.Context, creds Credentials, url string) (*http.Response, error) {
	resp, err := c.do(ctx, url, func(r *http.Request) {
		if creds.Username != "" {
			r.SetBasicAuth(creds.Username, creds.Password)
		}
	})
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusUnauthorized {
		return resp, nil
	}

	challenge := resp.Header.Get("WWW-Authenticate")
	resp.Body.Close()

	realm, params := ParseWWWAuthenticate(challenge)
	if realm == "" {
		// Nothing to negotiate with; return the original refusal rather than inventing one.
		return c.do(ctx, url, func(r *http.Request) {
			if creds.Username != "" {
				r.SetBasicAuth(creds.Username, creds.Password)
			}
		})
	}

	token, err := c.fetchToken(ctx, creds, realm, params)
	if err != nil {
		return nil, err
	}

	return c.do(ctx, url, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+token)
	})
}

func (c *Client) fetchToken(ctx context.Context, creds Credentials, realm string, params map[string]string) (string, error) {
	u, err := url.Parse(realm)
	if err != nil {
		return "", fmt.Errorf("registry named an unusable token endpoint: %w", err)
	}
	values := u.Query()
	for _, k := range []string{"service", "scope"} {
		if v, ok := params[k]; ok && v != "" {
			values.Set(k, v)
		}
	}
	u.RawQuery = values.Encode()

	resp, err := c.do(ctx, u.String(), func(r *http.Request) {
		if creds.Username != "" {
			r.SetBasicAuth(creds.Username, creds.Password)
		}
	})
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("the registry rejected these credentials")
	}

	var out struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", err
	}
	if out.Token != "" {
		return out.Token, nil
	}
	if out.AccessToken != "" {
		return out.AccessToken, nil
	}
	return "", fmt.Errorf("the registry returned no token")
}

func (c *Client) do(ctx context.Context, url string, decorate func(*http.Request)) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	decorate(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not reach the registry: %w", err)
	}
	return resp, nil
}

// ParseWWWAuthenticate pulls the realm and its parameters out of a Bearer challenge.
//
// The header looks like:
//
//	Bearer realm="https://auth.docker.io/token",service="registry.docker.io",scope="repository:x:pull"
//
// It is parsed by hand because the values are quoted, may contain commas inside the quotes
// (scopes do), and the scheme prefix is not part of any parameter.
func ParseWWWAuthenticate(header string) (realm string, params map[string]string) {
	params = map[string]string{}

	h := strings.TrimSpace(header)
	if i := strings.IndexByte(h, ' '); i >= 0 {
		if !strings.EqualFold(h[:i], "Bearer") {
			return "", params
		}
		h = h[i+1:]
	} else {
		return "", params
	}

	for len(h) > 0 {
		eq := strings.IndexByte(h, '=')
		if eq < 0 {
			break
		}
		key := strings.TrimSpace(h[:eq])
		h = h[eq+1:]

		var value string
		if strings.HasPrefix(h, `"`) {
			end := strings.IndexByte(h[1:], '"')
			if end < 0 {
				break
			}
			value = h[1 : end+1]
			h = h[end+2:]
		} else {
			end := strings.IndexByte(h, ',')
			if end < 0 {
				value, h = h, ""
			} else {
				value, h = h[:end], h[end:]
			}
		}

		params[key] = value
		h = strings.TrimLeft(h, ", ")
	}

	return params["realm"], params
}
