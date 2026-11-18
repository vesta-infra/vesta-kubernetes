package controllers

import (
	"encoding/json"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	vestav1alpha1 "kubernetes.getvesta.sh/operator/api/v1alpha1"
)

func TestForwardAuthCorazaWAF(t *testing.T) {
	raw := []byte(`{
  "forwardAuth": {
    "address": "http://coraza-traefik-middleware-waf.credpal-prod.svc.cluster.local:9080",
    "trustForwardHeader": true,
    "authRequestHeaders": [
      "X-Forwarded-Method","X-Forwarded-Proto","X-Forwarded-Host","X-Forwarded-Uri",
      "X-Forwarded-For","X-Forwarded-Port","X-Real-Ip","X-Remote-Ip","X-Host",
      "X-HTTP-Method","X-Rewrite-URL","X-Remote-Addr","X-Client-IP","X-Originating-IP",
      "True-Client-IP","CF-Connecting-IP","X-Cluster-Client-IP","Forwarded",
      "X-Original-URL","X-Method-Override","Content-Length","Content-Type",
      "User-Agent","Cookie","Referer"
    ]
  }
}`)

	got, err := compileMiddleware(vestav1alpha1.VestaMiddlewareSpec{
		Type: "raw",
		Raw:  &apiextensionsv1.JSON{Raw: raw},
	})
	if err != nil {
		t.Fatalf("forwardAuth failed to compile: %v", err)
	}

	fa, ok := got["forwardAuth"].(map[string]interface{})
	if !ok {
		t.Fatalf("forwardAuth key did not survive: %#v", got)
	}
	if fa["address"] != "http://coraza-traefik-middleware-waf.credpal-prod.svc.cluster.local:9080" {
		t.Errorf("address altered: %v", fa["address"])
	}
	if fa["trustForwardHeader"] != true {
		t.Errorf("trustForwardHeader altered: %v", fa["trustForwardHeader"])
	}

	headers, ok := fa["authRequestHeaders"].([]interface{})
	if !ok {
		t.Fatalf("authRequestHeaders missing: %#v", fa)
	}
	if len(headers) != 25 {
		t.Errorf("got %d headers, want all 25 -- a dropped header is one the WAF cannot inspect", len(headers))
	}
	// Order is not semantic here, but membership absolutely is: rule 1001952 checks the
	// IP-spoofing headers, and one silently dropped is one the WAF never sees.
	want := map[string]bool{"X-Client-IP": true, "True-Client-IP": true, "CF-Connecting-IP": true, "X-Originating-IP": true}
	for _, h := range headers {
		delete(want, h.(string))
	}
	if len(want) != 0 {
		t.Errorf("spoofing headers dropped: %v", want)
	}

	// Byte-for-byte round trip: whatever went in is what Traefik gets.
	var original map[string]interface{}
	json.Unmarshal(raw, &original)
	a, _ := json.Marshal(original)
	b, _ := json.Marshal(got)
	if string(a) != string(b) {
		t.Errorf("raw body was not passed through verbatim:\n in: %s\nout: %s", a, b)
	}
}
