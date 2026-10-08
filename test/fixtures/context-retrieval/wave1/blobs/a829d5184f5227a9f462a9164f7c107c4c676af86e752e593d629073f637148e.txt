package http

import "github.com/labstack/echo/v4"

type Error struct {
	Code    string      `json:"code"`
	Message string      `json:"message"`
	Details interface{} `json:"details,omitempty"`
}

type ErrorResponse struct {
	Error   Error  `json:"error"`
	TraceID string `json:"trace_id"`
}

type Response struct {
	Data interface{} `json:"data,omitempty"`
}

func JSON(c echo.Context, status int, data interface{}) error {
	return c.JSON(status, Response{Data: data})
}

func ErrorJSON(c echo.Context, status int, code, message, traceID string, details interface{}) error {
	return c.JSON(status, ErrorResponse{Error: Error{Code: code, Message: message, Details: details}, TraceID: traceID})
}
