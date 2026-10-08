package v1

import "github.com/labstack/echo/v4"

// Register reserves the administrative contour. Auth currently exposes no
// administrative HTTP routes, so keeping this router explicit prevents future
// admin endpoints from leaking into the public API contour.
func Register(_ *echo.Group) {}
