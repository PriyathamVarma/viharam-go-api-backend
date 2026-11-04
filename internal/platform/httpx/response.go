package httpx

import (
	"github.com/gin-gonic/gin"
	"net/http"
)

type Envelope struct {
	Success bool      `json:"success"`
	Data    any       `json:"data,omitempty"`
	Message string    `json:"message,omitempty"`
	Error   *APIError `json:"error,omitempty"`
}
type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func OK(c *gin.Context, data any, message string) error {
	c.JSON(http.StatusOK, Envelope{Success: true, Data: data, Message: message})
	return nil
}
func Created(c *gin.Context, data any, message string) error {
	c.JSON(http.StatusCreated, Envelope{Success: true, Data: data, Message: message})
	return nil
}
func Fail(c *gin.Context, status int, code, message string) error {
	c.JSON(status, Envelope{Success: false, Error: &APIError{Code: code, Message: message}, Message: message})
	return nil
}
