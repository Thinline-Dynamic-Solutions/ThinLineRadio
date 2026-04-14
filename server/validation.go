package main

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

var (
	// Email regex pattern - RFC 5322 compliant (simplified but practical)
	emailRegex = regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)

	// Max email length per RFC 5321
	maxEmailLength = 254
)

// ValidateEmail validates email format and length
// Returns normalized (lowercase) email if valid, error if invalid
func ValidateEmail(email string) error {
	if email == "" {
		return fmt.Errorf("email is required")
	}

	// Trim whitespace
	email = strings.TrimSpace(email)

	// Check length
	if len(email) > maxEmailLength {
		return fmt.Errorf("email must be 254 characters or less")
	}

	// Check format
	if !emailRegex.MatchString(email) {
		return fmt.Errorf("invalid email format")
	}

	// Additional checks
	if strings.HasPrefix(email, ".") || strings.HasPrefix(email, "@") {
		return fmt.Errorf("invalid email format")
	}

	if strings.Contains(email, "..") {
		return fmt.Errorf("invalid email format")
	}

	// Split to check domain
	parts := strings.Split(email, "@")
	if len(parts) != 2 {
		return fmt.Errorf("invalid email format")
	}

	domain := parts[1]
	if len(domain) == 0 || !strings.Contains(domain, ".") {
		return fmt.Errorf("invalid email format")
	}

	return nil
}

// NormalizeEmail converts email to lowercase for case-insensitive comparisons
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// PasswordStrength represents password strength requirements
type PasswordStrength struct {
	MinLength      int
	RequireUpper   bool
	RequireLower   bool
	RequireNumber  bool
	RequireSpecial bool
}

// DefaultPasswordStrength returns standard password requirements
func DefaultPasswordStrength() PasswordStrength {
	return PasswordStrength{
		MinLength:      8,
		RequireUpper:   true,
		RequireLower:   true,
		RequireNumber:  true,
		RequireSpecial: false, // Optional for better UX
	}
}

// ValidatePasswordStrength validates password against strength requirements
// Returns error message if invalid, nil if valid
func ValidatePasswordStrength(password string, strength PasswordStrength) error {
	if password == "" {
		return fmt.Errorf("password is required")
	}

	// Check minimum length
	if len(password) < strength.MinLength {
		return fmt.Errorf("password must be at least %d characters", strength.MinLength)
	}

	// Check maximum length (prevent DoS)
	if len(password) > 128 {
		return fmt.Errorf("password must be 128 characters or less")
	}

	var (
		hasUpper   = false
		hasLower   = false
		hasNumber  = false
		hasSpecial = false
	)

	// Check character requirements
	for _, char := range password {
		switch {
		case unicode.IsUpper(char):
			hasUpper = true
		case unicode.IsLower(char):
			hasLower = true
		case unicode.IsNumber(char):
			hasNumber = true
		case unicode.IsPunct(char) || unicode.IsSymbol(char):
			hasSpecial = true
		}
	}

	// Build error message for missing requirements
	var missing []string

	if strength.RequireUpper && !hasUpper {
		missing = append(missing, "uppercase letter")
	}
	if strength.RequireLower && !hasLower {
		missing = append(missing, "lowercase letter")
	}
	if strength.RequireNumber && !hasNumber {
		missing = append(missing, "number")
	}
	if strength.RequireSpecial && !hasSpecial {
		missing = append(missing, "special character")
	}

	if len(missing) > 0 {
		return fmt.Errorf("password must contain at least one %s", strings.Join(missing, ", "))
	}

	return nil
}

// ValidatePassword validates password with default strength requirements
func ValidatePassword(password string) error {
	return ValidatePasswordStrength(password, DefaultPasswordStrength())
}
