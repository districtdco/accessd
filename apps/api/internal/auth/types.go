package auth

import "time"

type User struct {
	ID          string
	Username    string
	Email       string
	DisplayName string
	Roles       []string
	CreatedAt   time.Time
}

type LoginResult struct {
	User User
}

type CurrentUser struct {
	ID           string
	Username     string
	Email        string
	DisplayName  string
	AuthProvider string
	Roles        []string
}

func (u CurrentUser) HasRole(role string) bool {
	for _, candidate := range u.Roles {
		if candidate == role {
			return true
		}
	}
	return false
}
