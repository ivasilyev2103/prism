package ingress

import (
	"errors"
	"net/http"
)

const tokenHeader = "X-Prism-Token"

var (
	ErrNoToken      = errors.New("ingress: missing X-Prism-Token header")
	ErrInvalidToken = errors.New("ingress: invalid token")
	ErrRateLimited  = errors.New("ingress: rate limit exceeded")
	ErrBadRequest   = errors.New("ingress: bad request")
)

// tokenValidator is an interface matching vault.Vault.ValidateToken.
// We depend on the interface, not the concrete vault implementation.
type tokenValidator interface {
	ValidateToken(token string) (projectID string, err error)
}

// authenticate extracts and validates the X-Prism-Token from the request.
func authenticate(r *http.Request, validator tokenValidator) (string, error) {
	token := r.Header.Get(tokenHeader)
	if token == "" {
		return "", ErrNoToken
	}

	projectID, err := validator.ValidateToken(token)
	if err != nil {
		return "", ErrInvalidToken
	}

	return projectID, nil
}
