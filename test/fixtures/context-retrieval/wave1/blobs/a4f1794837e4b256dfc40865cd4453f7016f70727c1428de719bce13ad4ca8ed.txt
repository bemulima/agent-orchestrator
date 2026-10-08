package middleware

import "github.com/labstack/echo/v4"

// RequestIDFromCtx extracts request ID from response or request headers.
func RequestIDFromCtx(c echo.Context) string {
	if reqID := c.Response().Header().Get(echo.HeaderXRequestID); reqID != "" {
		return reqID
	}
	return c.Request().Header.Get(echo.HeaderXRequestID)
}
