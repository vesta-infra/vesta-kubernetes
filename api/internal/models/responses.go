package models

import "time"

type ErrorResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Details string `json:"details,omitempty"`
}

type SetupStatusResponse struct {
	NeedsSetup bool `json:"needsSetup"`
}

type AuthTokenResponse struct {
	Token     string       `json:"token"`
	ExpiresAt string       `json:"expiresAt"`
	User      UserResponse `json:"user"`
}

type UserResponse struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	Email       string `json:"email"`
	DisplayName string `json:"displayName"`
	Role        string `json:"role"`
}

type APITokenCreatedResponse struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Token     string   `json:"token"`
	Scopes    []string `json:"scopes"`
	ExpiresAt string   `json:"expiresAt,omitempty"`
}

type APITokenResponse struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Scopes     []string `json:"scopes"`
	ExpiresAt  *string  `json:"expiresAt,omitempty"`
	LastUsedAt *string  `json:"lastUsedAt,omitempty"`
	CreatedAt  string   `json:"createdAt"`
}

type TeamResponse struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	MemberCount int    `json:"memberCount,omitempty"`
	CreatedAt   string `json:"createdAt"`
}

type TeamMemberResponse struct {
	UserID      string `json:"userId"`
	Username    string `json:"username"`
	Email       string `json:"email"`
	DisplayName string `json:"displayName"`
	Role        string `json:"role"`
	JoinedAt    string `json:"joinedAt"`
}

type DeployResponse struct {
	ID          string `json:"id"`
	AppID       string `json:"appId"`
	Status      string `json:"status"`
	Version     int    `json:"version,omitempty"`
	Image       string `json:"image,omitempty"`
	TriggeredBy string `json:"triggeredBy,omitempty"`
	TriggeredAt string `json:"triggeredAt"`
	StatusURL   string `json:"statusUrl"`
}

type ListResponse struct {
	Items interface{} `json:"items"`
	Total int         `json:"total"`
}

type BuildResponse struct {
	ID          string  `json:"id"`
	AppID       string  `json:"appId"`
	ProjectID   string  `json:"projectId"`
	Environment string  `json:"environment"`
	Status      string  `json:"status"`
	Strategy    string  `json:"strategy"`
	CommitSHA   string  `json:"commitSha"`
	Branch      string  `json:"branch"`
	Repository  string  `json:"repository"`
	Image       string  `json:"image"`
	TriggeredBy string  `json:"triggeredBy"`
	StartedAt   *string `json:"startedAt,omitempty"`
	FinishedAt  *string `json:"finishedAt,omitempty"`
	DurationMs  int     `json:"durationMs"`
	Error       string  `json:"error,omitempty"`
	LogsURL     string  `json:"logsUrl"`
	CreatedAt   string  `json:"createdAt"`
}

func NowRFC3339() string {
	return time.Now().UTC().Format(time.RFC3339)
}
