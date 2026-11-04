package auth

type User struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Email       string `json:"email"`
	Role        string `json:"type"`
	PhoneNumber string `json:"phoneNumber,omitempty"`
	PhotoURL    string `json:"photo,omitempty"`
	Bio         string `json:"bio,omitempty"`
	IsVerified  bool   `json:"isVerified"`
}

type RegisterInput struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"`
	Type     string `json:"type"`
}

type LoginInput struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}
