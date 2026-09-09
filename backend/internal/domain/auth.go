package domain

// LogInRequest is the wire-format shape of a POST /login request body.
type LogInRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}
