package auth

import "errors"

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrUnauthorized       = errors.New("unauthorized")
	ErrForbidden          = errors.New("forbidden")
	ErrUserNotFound       = errors.New("user not found")
	ErrRateLimited        = errors.New("too many failed login attempts")
	ErrPasswordChangeNotAllowed = errors.New("password change is only available for local authentication users")
	ErrInvalidCurrentPassword   = errors.New("current password is incorrect")
	ErrInvalidNewPassword       = errors.New("new password must be at least 8 characters")
)
