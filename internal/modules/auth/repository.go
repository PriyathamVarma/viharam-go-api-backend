package auth

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/viharam/go-api-backend/internal/platform/database"
)

var ErrNotFound = errors.New("user not found")
var ErrEmailExists = errors.New("email already registered")
var ErrPhoneExists = errors.New("phone number already registered")

type UserRepository interface {
	Create(context.Context, RegisterInput, string) (User, error)
	EnsureDemo(context.Context, RegisterInput, string) (User, error)
	FindByEmail(context.Context, string) (User, string, error)
	FindByID(context.Context, string) (User, error)
	FindOrCreateOAuth(context.Context, string, string, string, string) (User, error)
	Delete(context.Context, string) error
	UpdatePhone(context.Context, string, string) (User, error)
}

func (r *Repository) Delete(ctx context.Context, id string) error {
	command, err := r.db.Pool.Exec(ctx, `UPDATE users SET status='deleted', updated_at=now() WHERE id=$1::uuid AND status='active'`, id)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

type Repository struct{ db *database.Postgres }

func NewRepository(db *database.Postgres) *Repository { return &Repository{db: db} }

func (r *Repository) Create(ctx context.Context, input RegisterInput, passwordHash string) (User, error) {
	var user User
	err := r.db.Pool.QueryRow(ctx, `INSERT INTO users(name,email,password_hash,role,phone_number) VALUES($1,$2,$3,$4,$5) RETURNING id::text,name,email,role,COALESCE(phone_number,''),COALESCE(photo_url,''),COALESCE(bio,''),is_verified`, input.Name, strings.ToLower(strings.TrimSpace(input.Email)), passwordHash, input.Type, input.Phone).Scan(&user.ID, &user.Name, &user.Email, &user.Role, &user.PhoneNumber, &user.PhotoURL, &user.Bio, &user.IsVerified)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "users_email_lower_unique") {
			return User{}, ErrEmailExists
		}
		if strings.Contains(strings.ToLower(err.Error()), "users_phone_unique") {
			return User{}, ErrPhoneExists
		}
		return User{}, err
	}
	return user, nil
}

func (r *Repository) UpdatePhone(ctx context.Context, id, phone string) (User, error) {
	var user User
	err := r.db.Pool.QueryRow(ctx, `UPDATE users SET phone_number=$2,updated_at=now() WHERE id=$1::uuid AND status='active' RETURNING id::text,name,email,role,COALESCE(phone_number,''),COALESCE(photo_url,''),COALESCE(bio,''),is_verified`, id, phone).Scan(&user.ID, &user.Name, &user.Email, &user.Role, &user.PhoneNumber, &user.PhotoURL, &user.Bio, &user.IsVerified)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "users_phone_unique") {
			return User{}, ErrPhoneExists
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return User{}, ErrNotFound
		}
		return User{}, err
	}
	return user, nil
}

// EnsureDemo creates or repairs a development demo account. It is separate
// from Create so a stale account cannot break a demo shortcut.
func (r *Repository) EnsureDemo(ctx context.Context, input RegisterInput, passwordHash string) (User, error) {
	var user User
	err := r.db.Pool.QueryRow(ctx, `
		INSERT INTO users(name,email,password_hash,role,status)
		VALUES($1,$2,$3,$4,'active')
		ON CONFLICT (lower(email)) DO UPDATE SET
			name=EXCLUDED.name,
			password_hash=EXCLUDED.password_hash,
			role=EXCLUDED.role,
			status='active',
			updated_at=now()
		RETURNING id::text,name,email,role,COALESCE(phone_number,''),COALESCE(photo_url,''),COALESCE(bio,''),is_verified`,
		input.Name, strings.ToLower(strings.TrimSpace(input.Email)), passwordHash, input.Type,
	).Scan(&user.ID, &user.Name, &user.Email, &user.Role, &user.PhoneNumber, &user.PhotoURL, &user.Bio, &user.IsVerified)
	if err != nil {
		return User{}, err
	}
	return user, nil
}

func (r *Repository) FindByEmail(ctx context.Context, email string) (User, string, error) {
	var user User
	var passwordHash string
	err := r.db.Pool.QueryRow(ctx, `SELECT id::text,name,email,password_hash,role,COALESCE(phone_number,''),COALESCE(photo_url,''),COALESCE(bio,''),is_verified FROM users WHERE lower(email)=lower($1) AND status='active'`, strings.TrimSpace(email)).Scan(&user.ID, &user.Name, &user.Email, &passwordHash, &user.Role, &user.PhoneNumber, &user.PhotoURL, &user.Bio, &user.IsVerified)
	if err != nil {
		return User{}, "", ErrNotFound
	}
	return user, passwordHash, nil
}

func (r *Repository) FindByID(ctx context.Context, id string) (User, error) {
	var user User
	err := r.db.Pool.QueryRow(ctx, `SELECT id::text,name,email,role,COALESCE(phone_number,''),COALESCE(photo_url,''),COALESCE(bio,''),is_verified FROM users WHERE id=$1::uuid AND status='active'`, id).Scan(&user.ID, &user.Name, &user.Email, &user.Role, &user.PhoneNumber, &user.PhotoURL, &user.Bio, &user.IsVerified)
	if err != nil {
		return User{}, ErrNotFound
	}
	return user, nil
}

func (r *Repository) FindOrCreateOAuth(ctx context.Context, provider, subject, name, email string) (User, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	var user User
	err := r.db.Pool.QueryRow(ctx, `SELECT id::text,name,email,role,COALESCE(phone_number,''),COALESCE(photo_url,''),COALESCE(bio,''),is_verified FROM users WHERE auth_provider=$1 AND auth_subject=$2 AND status='active'`, provider, subject).Scan(&user.ID, &user.Name, &user.Email, &user.Role, &user.PhoneNumber, &user.PhotoURL, &user.Bio, &user.IsVerified)
	if err == nil {
		return user, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return User{}, err
	}
	var existingID string
	err = r.db.Pool.QueryRow(ctx, `SELECT id::text FROM users WHERE lower(email)=lower($1) AND status='active'`, email).Scan(&existingID)
	if err == nil {
		return User{}, errors.New("an account already exists for this email")
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return User{}, err
	}
	err = r.db.Pool.QueryRow(ctx, `INSERT INTO users(name,email,password_hash,role,auth_provider,auth_subject) VALUES($1,$2,'oauth:'||gen_random_uuid(),'Traveller',$3,$4) RETURNING id::text,name,email,role,COALESCE(phone_number,''),COALESCE(photo_url,''),COALESCE(bio,''),is_verified`, name, email, provider, subject).Scan(&user.ID, &user.Name, &user.Email, &user.Role, &user.PhoneNumber, &user.PhotoURL, &user.Bio, &user.IsVerified)
	if err != nil {
		return User{}, err
	}
	return user, nil
}
