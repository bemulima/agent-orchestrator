package handlers

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/example/auth-service/internal/usecase"
	res "github.com/example/auth-service/pkg/http"
)

type AuthHandler struct {
	service usecase.Service
}

func NewAuthHandler(s usecase.Service) *AuthHandler { return &AuthHandler{service: s} }

type signupStartRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type signupVerifyRequest struct {
	Email string `json:"email"`
	Code  string `json:"code"`
}

type signinRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

type revokeRefreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

type emailChangeStartRequest struct {
	NewEmail string `json:"new_email"`
}

type emailChangeVerifyRequest struct {
	Code string `json:"code"`
}

type passwordResetStartRequest struct {
	Email string `json:"email"`
}

type passwordResetFinishRequest struct {
	Email       string `json:"email"`
	Code        string `json:"code"`
	NewPassword string `json:"new_password"`
}

type changePasswordRequest struct {
	OldPassword string `json:"old_password"`
	NewPassword string `json:"new_password"`
}

type setPasswordRequest struct {
	NewPassword string `json:"new_password"`
}

type oauthStartRequest struct {
	ReturnTo string `json:"return_to"`
}

type oauthCallbackRequest struct {
	Code  string `json:"code"`
	State string `json:"state"`
}

type verifyTokenRequest struct {
	Token string `json:"token"`
}

func (h *AuthHandler) SignupStart(c echo.Context) error {
	req := new(signupStartRequest)
	if err := c.Bind(req); err != nil {
		return res.ErrorJSON(c, http.StatusBadRequest, "bad_request", "invalid payload", requestIDFromCtx(c), nil)
	}
	if err := h.service.StartSignup(c.Request().Context(), requestIDFromCtx(c), req.Email, req.Password); err != nil {
		return res.ErrorJSON(c, http.StatusBadRequest, "signup_failed", err.Error(), requestIDFromCtx(c), nil)
	}
	return c.JSON(http.StatusAccepted, map[string]string{"message": "verification code sent"})
}

func (h *AuthHandler) SignupVerify(c echo.Context) error {
	req := new(signupVerifyRequest)
	if err := c.Bind(req); err != nil {
		return res.ErrorJSON(c, http.StatusBadRequest, "bad_request", "invalid payload", requestIDFromCtx(c), nil)
	}
	_, tokens, err := h.service.VerifySignup(c.Request().Context(), requestIDFromCtx(c), req.Email, req.Code)
	if err != nil {
		return res.ErrorJSON(c, http.StatusBadRequest, "verification_failed", err.Error(), requestIDFromCtx(c), nil)
	}
	return c.JSON(http.StatusOK, tokens)
}

func (h *AuthHandler) SignIn(c echo.Context) error {
	req := new(signinRequest)
	if err := c.Bind(req); err != nil {
		return res.ErrorJSON(c, http.StatusBadRequest, "bad_request", "invalid payload", requestIDFromCtx(c), nil)
	}
	_, tokens, err := h.service.SignIn(c.Request().Context(), requestIDFromCtx(c), req.Email, req.Password)
	if err != nil {
		return res.ErrorJSON(c, http.StatusUnauthorized, "signin_failed", err.Error(), requestIDFromCtx(c), nil)
	}
	return c.JSON(http.StatusOK, tokens)
}

func (h *AuthHandler) Refresh(c echo.Context) error {
	req := new(refreshRequest)
	if err := c.Bind(req); err != nil {
		return res.ErrorJSON(c, http.StatusBadRequest, "bad_request", "invalid payload", requestIDFromCtx(c), nil)
	}
	tokens, err := h.service.Refresh(c.Request().Context(), requestIDFromCtx(c), req.RefreshToken)
	if err != nil {
		return res.ErrorJSON(c, http.StatusUnauthorized, "refresh_failed", err.Error(), requestIDFromCtx(c), nil)
	}
	return c.JSON(http.StatusOK, tokens)
}

func (h *AuthHandler) RevokeRefresh(c echo.Context) error {
	req := new(revokeRefreshRequest)
	if err := c.Bind(req); err != nil {
		return res.ErrorJSON(c, http.StatusBadRequest, "bad_request", "invalid payload", requestIDFromCtx(c), nil)
	}
	if err := h.service.RevokeRefreshToken(c.Request().Context(), requestIDFromCtx(c), req.RefreshToken); err != nil {
		return res.ErrorJSON(c, http.StatusUnauthorized, "revoke_failed", err.Error(), requestIDFromCtx(c), nil)
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *AuthHandler) EmailChangeStart(c echo.Context) error {
	req := new(emailChangeStartRequest)
	if err := c.Bind(req); err != nil {
		return res.ErrorJSON(c, http.StatusBadRequest, "bad_request", "invalid payload", requestIDFromCtx(c), nil)
	}
	userID := c.Get("user_id").(string)
	_, err := h.service.StartEmailChange(c.Request().Context(), requestIDFromCtx(c), userID, req.NewEmail)
	if err != nil {
		return res.ErrorJSON(c, http.StatusBadRequest, "email_change_failed", err.Error(), requestIDFromCtx(c), nil)
	}
	return c.JSON(http.StatusOK, map[string]string{"message": "verification code sent to new email"})
}

func (h *AuthHandler) GetMe(c echo.Context) error {
	userID := c.Get("user_id").(string)
	me, err := h.service.GetMe(c.Request().Context(), requestIDFromCtx(c), userID)
	if err != nil {
		return res.ErrorJSON(c, http.StatusNotFound, "not_found", err.Error(), requestIDFromCtx(c), nil)
	}
	return res.JSON(c, http.StatusOK, me)
}

func (h *AuthHandler) EmailChangeVerify(c echo.Context) error {
	req := new(emailChangeVerifyRequest)
	if err := c.Bind(req); err != nil {
		return res.ErrorJSON(c, http.StatusBadRequest, "bad_request", "invalid payload", requestIDFromCtx(c), nil)
	}
	user, err := h.service.VerifyEmailChange(c.Request().Context(), requestIDFromCtx(c), req.Code)
	if err != nil {
		return res.ErrorJSON(c, http.StatusBadRequest, "email_change_failed", err.Error(), requestIDFromCtx(c), nil)
	}
	return res.JSON(c, http.StatusOK, user)
}

func (h *AuthHandler) PasswordResetStart(c echo.Context) error {
	req := new(passwordResetStartRequest)
	if err := c.Bind(req); err != nil {
		return res.ErrorJSON(c, http.StatusBadRequest, "bad_request", "invalid payload", requestIDFromCtx(c), nil)
	}
	uuid, err := h.service.StartPasswordReset(c.Request().Context(), requestIDFromCtx(c), req.Email)
	if err != nil {
		return res.ErrorJSON(c, http.StatusBadRequest, "password_reset_failed", err.Error(), requestIDFromCtx(c), nil)
	}
	return res.JSON(c, http.StatusAccepted, map[string]string{"uuid": uuid})
}

func (h *AuthHandler) PasswordResetFinish(c echo.Context) error {
	req := new(passwordResetFinishRequest)
	if err := c.Bind(req); err != nil {
		return res.ErrorJSON(c, http.StatusBadRequest, "bad_request", "invalid payload", requestIDFromCtx(c), nil)
	}
	if err := h.service.FinishPasswordReset(c.Request().Context(), requestIDFromCtx(c), req.Email, req.Code, req.NewPassword); err != nil {
		return res.ErrorJSON(c, http.StatusBadRequest, "password_reset_failed", err.Error(), requestIDFromCtx(c), nil)
	}
	return res.JSON(c, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *AuthHandler) ChangePassword(c echo.Context) error {
	req := new(changePasswordRequest)
	if err := c.Bind(req); err != nil {
		return res.ErrorJSON(c, http.StatusBadRequest, "bad_request", "invalid payload", requestIDFromCtx(c), nil)
	}
	userID := c.Get("user_id").(string)
	if err := h.service.ChangePassword(c.Request().Context(), requestIDFromCtx(c), userID, req.OldPassword, req.NewPassword); err != nil {
		return res.ErrorJSON(c, http.StatusBadRequest, "password_change_failed", err.Error(), requestIDFromCtx(c), nil)
	}
	return res.JSON(c, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *AuthHandler) SetPassword(c echo.Context) error {
	req := new(setPasswordRequest)
	if err := c.Bind(req); err != nil {
		return res.ErrorJSON(c, http.StatusBadRequest, "bad_request", "invalid payload", requestIDFromCtx(c), nil)
	}
	userID := c.Get("user_id").(string)
	if err := h.service.SetPassword(c.Request().Context(), requestIDFromCtx(c), userID, req.NewPassword); err != nil {
		return res.ErrorJSON(c, http.StatusBadRequest, "password_set_failed", err.Error(), requestIDFromCtx(c), nil)
	}
	return res.JSON(c, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *AuthHandler) ListIdentities(c echo.Context) error {
	userID := c.Get("user_id").(string)
	identities, err := h.service.ListIdentities(c.Request().Context(), requestIDFromCtx(c), userID)
	if err != nil {
		return res.ErrorJSON(c, http.StatusBadRequest, "identity_list_failed", err.Error(), requestIDFromCtx(c), nil)
	}
	return res.JSON(c, http.StatusOK, map[string]interface{}{"identities": identities})
}

func (h *AuthHandler) RemoveIdentity(c echo.Context) error {
	userID := c.Get("user_id").(string)
	if err := h.service.RemoveIdentity(c.Request().Context(), requestIDFromCtx(c), userID, c.Param("provider"), c.Param("provider_user_id")); err != nil {
		return res.ErrorJSON(c, http.StatusBadRequest, "identity_remove_failed", err.Error(), requestIDFromCtx(c), nil)
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *AuthHandler) OAuthStart(c echo.Context) error {
	provider := c.Param("provider")
	req := new(oauthStartRequest)
	if err := c.Bind(req); err != nil {
		return res.ErrorJSON(c, http.StatusBadRequest, "bad_request", "invalid payload", requestIDFromCtx(c), nil)
	}
	result, err := h.service.OAuthStart(c.Request().Context(), requestIDFromCtx(c), provider, req.ReturnTo)
	if err != nil {
		return res.ErrorJSON(c, http.StatusBadRequest, "oauth_start_failed", err.Error(), requestIDFromCtx(c), nil)
	}
	return c.JSON(http.StatusOK, result)
}

func (h *AuthHandler) OAuthCallback(c echo.Context) error {
	provider := c.Param("provider")
	req := new(oauthCallbackRequest)
	if err := c.Bind(req); err != nil {
		return res.ErrorJSON(c, http.StatusBadRequest, "bad_request", "invalid payload", requestIDFromCtx(c), nil)
	}

	_, tokens, err := h.service.OAuthCallback(c.Request().Context(), requestIDFromCtx(c), provider, req.Code, req.State)
	if err != nil {
		return res.ErrorJSON(c, http.StatusBadRequest, "oauth_failed", err.Error(), requestIDFromCtx(c), nil)
	}
	return c.JSON(http.StatusOK, tokens)
}

func (h *AuthHandler) VerifyToken(c echo.Context) error {
	req := new(verifyTokenRequest)
	if err := c.Bind(req); err != nil {
		return res.ErrorJSON(c, http.StatusBadRequest, "bad_request", "invalid payload", requestIDFromCtx(c), nil)
	}
	result, err := h.service.VerifyToken(c.Request().Context(), requestIDFromCtx(c), req.Token)
	if err != nil {
		return res.ErrorJSON(c, http.StatusUnauthorized, "verify_failed", err.Error(), requestIDFromCtx(c), nil)
	}
	return c.JSON(http.StatusOK, map[string]interface{}{
		"user_id": result.UserID,
		"email":   result.Email,
		"claims":  result.Claims,
	})
}

func requestIDFromCtx(c echo.Context) string {
	if reqID := c.Response().Header().Get(echo.HeaderXRequestID); reqID != "" {
		return reqID
	}
	return c.Request().Header.Get(echo.HeaderXRequestID)
}
