package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/bcrypt"
)

const (
	argon2MemoryKiB = 19 * 1024
	argon2Time      = 2
	argon2Threads   = 1
	argon2KeyLen    = 32
	argon2SaltLen   = 16
)

type Service struct {
	repo        UserRepository
	secret      []byte
	expires     time.Duration
	supabaseURL string
	supabaseKey string
}

func NewService(repo UserRepository, secret string, expires time.Duration, supabaseURL, supabaseKey string) *Service {
	return &Service{repo: repo, secret: []byte(secret), expires: expires, supabaseURL: supabaseURL, supabaseKey: supabaseKey}
}

func (s *Service) Register(ctx context.Context, input RegisterInput) (User, string, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.Email = strings.ToLower(strings.TrimSpace(input.Email))
	input.Type = strings.TrimSpace(input.Type)
	if input.Name == "" || input.Email == "" || len(input.Password) < 8 {
		return User{}, "", errors.New("name, valid email and password of at least 8 characters are required")
	}
	if input.Type != "Host" && input.Type != "Ambassador" {
		input.Type = "Traveller"
	}
	hash, err := hashPassword(input.Password)
	if err != nil {
		return User{}, "", err
	}
	user, err := s.repo.Create(ctx, input, string(hash))
	if err != nil {
		return User{}, "", err
	}
	token, err := s.token(user)
	return user, token, err
}

func (s *Service) Login(ctx context.Context, input LoginInput) (User, string, error) {
	user, hash, err := s.repo.FindByEmail(ctx, input.Email)
	if err != nil {
		return User{}, "", errors.New("invalid email or password")
	}
	valid := verifyPassword(input.Password, hash)
	if strings.HasPrefix(hash, "$2") {
		// Existing Next.js users have bcrypt hashes. Keep them readable during
		// migration; newly registered users are always stored as Argon2id.
		valid = bcrypt.CompareHashAndPassword([]byte(hash), []byte(input.Password)) == nil
	}
	if !valid {
		return User{}, "", errors.New("invalid email or password")
	}
	token, err := s.token(user)
	return user, token, err
}

func hashPassword(password string) (string, error) {
	salt := make([]byte, argon2SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	derived := argon2.IDKey([]byte(password), salt, argon2Time, argon2MemoryKiB, argon2Threads, argon2KeyLen)
	enc := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", argon2MemoryKiB, argon2Time, argon2Threads, enc.EncodeToString(salt), enc.EncodeToString(derived)), nil
}

func verifyPassword(password, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v=19" {
		return false
	}
	params := strings.Split(parts[3], ",")
	if len(params) != 3 || params[0] != fmt.Sprintf("m=%d", argon2MemoryKiB) || params[1] != fmt.Sprintf("t=%d", argon2Time) || params[2] != fmt.Sprintf("p=%d", argon2Threads) {
		return false
	}
	enc := base64.RawStdEncoding
	salt, err := enc.DecodeString(parts[4])
	if err != nil || len(salt) != argon2SaltLen {
		return false
	}
	want, err := enc.DecodeString(parts[5])
	if err != nil || len(want) != argon2KeyLen {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, argon2Time, argon2MemoryKiB, argon2Threads, argon2KeyLen)
	return len(got) == len(want) && subtle.ConstantTimeCompare(got, want) == 1
}

func (s *Service) token(user User) (string, error) {
	now := time.Now()
	return jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"sub": user.ID, "email": user.Email, "type": user.Role, "iat": now.Unix(), "exp": now.Add(s.expires).Unix()}).SignedString(s.secret)
}
