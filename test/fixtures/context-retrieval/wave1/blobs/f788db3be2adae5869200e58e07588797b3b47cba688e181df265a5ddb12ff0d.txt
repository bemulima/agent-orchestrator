// Package handlers implements the public user HTTP endpoints.
package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/example/user-service/internal/domain"
	"github.com/example/user-service/internal/infrastructure/filestorage"
	"github.com/example/user-service/internal/infrastructure/imageprocessor"
	"github.com/example/user-service/internal/transport/http/middleware"
	service "github.com/example/user-service/internal/usecase"
	res "github.com/example/user-service/pkg/http"
)

// Handler serves public user profile and avatar requests.
type Handler struct {
	users        service.UserService
	storage      filestorage.Client
	imageProc    imageprocessor.Client
	avatarPreset string
	avatarKind   string
}

// NewHandler creates the public user handler and its avatar dependencies.
func NewHandler(users service.UserService, storage filestorage.Client, imgProc imageprocessor.Client, avatarPreset, avatarKind string) *Handler {
	return &Handler{users: users, storage: storage, imageProc: imgProc, avatarPreset: avatarPreset, avatarKind: avatarKind}
}

type updateProfileRequest struct {
	DisplayName *string `json:"display_name"`
}

type userResponse struct {
	ID           string             `json:"id"`
	Email        string             `json:"email"`
	Status       *domain.UserStatus `json:"status,omitempty"`
	IsActive     *bool              `json:"is_active,omitempty"`
	DisplayName  *string            `json:"display_name,omitempty"`
	FirstName    *string            `json:"first_name,omitempty"`
	LastName     *string            `json:"last_name,omitempty"`
	BirthYear    *int               `json:"birth_year,omitempty"`
	Gender       *string            `json:"gender,omitempty"`
	AvatarFileID *string            `json:"avatar_file_id,omitempty"`
	AvatarURL    *string            `json:"avatar_url,omitempty"`
	CreatedAt    time.Time          `json:"created_at"`
	UpdatedAt    time.Time          `json:"updated_at"`
}

type profileResponse struct {
	ID           string    `json:"id"`
	UserID       string    `json:"user_id"`
	DisplayName  *string   `json:"display_name,omitempty"`
	FirstName    *string   `json:"first_name,omitempty"`
	LastName     *string   `json:"last_name,omitempty"`
	BirthYear    *int      `json:"birth_year,omitempty"`
	Gender       *string   `json:"gender,omitempty"`
	AvatarFileID *string   `json:"avatar_file_id,omitempty"`
	AvatarURL    *string   `json:"avatar_url,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type attachIdentityRequest struct {
	Provider       string  `json:"provider"`
	ProviderUserID string  `json:"provider_user_id"`
	Email          string  `json:"email"`
	DisplayName    *string `json:"display_name"`
	AvatarURL      *string `json:"avatar_url"`
}

// RegisterRoutes attaches the public user routes to g.
func (h *Handler) RegisterRoutes(g *echo.Group) {
	g.GET("/me", h.GetMe)
	g.GET("/:id", h.GetByID)
	g.PATCH("/me", h.UpdateProfile)
	g.POST("/me/avatar", h.UploadAvatar)
	g.GET("/me/identities", h.ListMyIdentities)
	g.POST("/me/identities", h.AttachIdentity)
	g.DELETE("/me/identities/:provider/:provider_user_id", h.RemoveIdentity)
}

// GetMe returns the authenticated user's profile.
func (h *Handler) GetMe(c echo.Context) error {
	userID := c.Get("user_id").(string)
	user, err := h.users.GetMe(c.Request().Context(), userID)
	if err != nil {
		return res.ErrorJSON(c, http.StatusNotFound, "not_found", "user not found", middleware.RequestIDFromCtx(c), nil)
	}
	return res.JSON(c, http.StatusOK, h.newSelfUserResponse(user))
}

// GetByID returns the public profile for the requested user.
func (h *Handler) GetByID(c echo.Context) error {
	userID := c.Param("id")
	requester := c.Get("user_id").(string)
	user, err := h.users.GetByID(c.Request().Context(), requester, userID)
	if err != nil {
		return res.ErrorJSON(c, http.StatusNotFound, "not_found", "user not found", middleware.RequestIDFromCtx(c), nil)
	}
	return res.JSON(c, http.StatusOK, h.newPublicUserResponse(user))
}

// UpdateProfile changes the authenticated user's editable profile fields.
func (h *Handler) UpdateProfile(c echo.Context) error {
	var req updateProfileRequest
	decoder := json.NewDecoder(c.Request().Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		return res.ErrorJSON(c, http.StatusBadRequest, "bad_request", "invalid payload", middleware.RequestIDFromCtx(c), nil)
	}
	if decoder.More() {
		return res.ErrorJSON(c, http.StatusBadRequest, "bad_request", "invalid payload", middleware.RequestIDFromCtx(c), nil)
	}
	userID := c.Get("user_id").(string)
	profile, err := h.users.UpdateProfile(c.Request().Context(), userID, req.DisplayName)
	if err != nil {
		return res.ErrorJSON(c, http.StatusInternalServerError, "update_failed", err.Error(), middleware.RequestIDFromCtx(c), nil)
	}
	return res.JSON(c, http.StatusOK, h.newProfileResponse(profile))
}

// AttachIdentity links a provider identity to the authenticated user.
func (h *Handler) AttachIdentity(c echo.Context) error {
	req := new(attachIdentityRequest)
	if err := c.Bind(req); err != nil {
		return res.ErrorJSON(c, http.StatusBadRequest, "bad_request", "invalid payload", middleware.RequestIDFromCtx(c), nil)
	}
	provider := domain.IdentityProvider(strings.ToLower(req.Provider))
	userID := c.Get("user_id").(string)
	identity, profile, err := h.users.AttachIdentity(c.Request().Context(), userID, provider, req.ProviderUserID, req.Email, req.DisplayName, req.AvatarURL)
	if err != nil {
		return res.ErrorJSON(c, http.StatusBadRequest, "attach_failed", err.Error(), middleware.RequestIDFromCtx(c), nil)
	}
	return res.JSON(c, http.StatusCreated, map[string]interface{}{"identity": identity, "profile": h.decorateProfile(profile)})
}

// RemoveIdentity detaches the selected provider identity from the user.
func (h *Handler) RemoveIdentity(c echo.Context) error {
	provider := domain.IdentityProvider(strings.ToLower(c.Param("provider")))
	providerUserID := c.Param("provider_user_id")
	userID := c.Get("user_id").(string)
	if err := h.users.RemoveIdentity(c.Request().Context(), userID, provider, providerUserID); err != nil {
		return res.ErrorJSON(c, http.StatusBadRequest, "detach_failed", err.Error(), middleware.RequestIDFromCtx(c), nil)
	}
	return res.JSON(c, http.StatusOK, map[string]string{"status": "detached"})
}

// ListMyIdentities returns the identities linked to the authenticated user.
func (h *Handler) ListMyIdentities(c echo.Context) error {
	userID := c.Get("user_id").(string)
	identities, err := h.users.ListIdentities(c.Request().Context(), userID)
	if err != nil {
		return res.ErrorJSON(c, http.StatusInternalServerError, "list_failed", err.Error(), middleware.RequestIDFromCtx(c), nil)
	}
	return res.JSON(c, http.StatusOK, map[string]any{"identities": identities})
}

const maxAvatarSize = 5 * 1024 * 1024

// UploadAvatar stores the authenticated user's avatar and returns its file ID.
func (h *Handler) UploadAvatar(c echo.Context) error {
	fileHeader, err := c.FormFile("file")
	if err != nil {
		return res.ErrorJSON(c, http.StatusBadRequest, "bad_request", "file is required", middleware.RequestIDFromCtx(c), nil)
	}
	src, err := fileHeader.Open()
	if err != nil {
		return res.ErrorJSON(c, http.StatusBadRequest, "bad_request", "file open failed", middleware.RequestIDFromCtx(c), nil)
	}
	defer func() { _ = src.Close() }()

	data, err := io.ReadAll(io.LimitReader(src, maxAvatarSize+1))
	if err != nil {
		return res.ErrorJSON(c, http.StatusBadRequest, "bad_request", "file read failed", middleware.RequestIDFromCtx(c), nil)
	}
	if len(data) > maxAvatarSize {
		return res.ErrorJSON(c, http.StatusBadRequest, "bad_request", "file too large", middleware.RequestIDFromCtx(c), nil)
	}

	userID := c.Get("user_id").(string)
	processingMode := strings.ToUpper(strings.TrimSpace(c.FormValue("processing_mode")))
	if processingMode == "" {
		processingMode = "DISABLED"
	}
	switch processingMode {
	case "EAGER", "LAZY", "DISABLED":
	default:
		return res.ErrorJSON(c, http.StatusBadRequest, "bad_request", "invalid processing_mode", middleware.RequestIDFromCtx(c), nil)
	}

	uploadResp, err := h.storage.Upload(c.Request().Context(), filestorage.UploadRequest{
		OwnerID:        userID,
		FileKind:       h.avatarKind,
		ProcessingMode: processingMode,
		FileName:       fileHeader.Filename,
		ContentType:    fileHeader.Header.Get(echo.HeaderContentType),
		Data:           data,
	})
	if err != nil {
		return res.ErrorJSON(c, http.StatusBadRequest, "upload_failed", "avatar upload failed", middleware.RequestIDFromCtx(c), nil)
	}

	profile, err := h.users.SetAvatarFileID(c.Request().Context(), userID, uploadResp.ID)
	if err != nil {
		return res.ErrorJSON(c, http.StatusInternalServerError, "update_failed", err.Error(), middleware.RequestIDFromCtx(c), nil)
	}

	if processingMode == "EAGER" && h.imageProc != nil {
		delegationClient, ok := h.storage.(filestorage.ProcessingDelegationClient)
		if !ok {
			return res.ErrorJSON(c, http.StatusInternalServerError, "processing_failed", "avatar processing failed", middleware.RequestIDFromCtx(c), nil)
		}
		processingDelegation, err := delegationClient.CreateProcessingDelegation(
			c.Request().Context(), uploadResp.ID, userID, "image_processor", "read_source", 0,
		)
		if err != nil {
			return res.ErrorJSON(c, http.StatusInternalServerError, "processing_failed", "avatar processing failed", middleware.RequestIDFromCtx(c), nil)
		}
		if processingDelegation == "" {
			return res.ErrorJSON(c, http.StatusInternalServerError, "processing_failed", "avatar processing failed", middleware.RequestIDFromCtx(c), nil)
		}
		if err := h.imageProc.GenerateWithDelegation(c.Request().Context(), uploadResp.ID, userID, h.avatarKind, h.avatarPreset, nil, processingDelegation); err != nil {
			return res.ErrorJSON(c, http.StatusInternalServerError, "processing_failed", "avatar processing failed", middleware.RequestIDFromCtx(c), nil)
		}
	}

	signedURL, _ := h.storage.SignedURL(c.Request().Context(), uploadResp.ID, 15)
	// Private FileStorage proxy URLs require a service credential. Return the
	// short-lived capability issued after this authenticated User flow instead.
	downloadURL := signedURL

	response := map[string]interface{}{
		"file_id":         uploadResp.ID,
		"download_url":    downloadURL,
		"signed_url":      signedURL,
		"profile":         h.newProfileResponse(profile),
		"processing_mode": processingMode,
	}
	return res.JSON(c, http.StatusCreated, response)
}

func (h *Handler) newSelfUserResponse(user *domain.User) *userResponse {
	return h.newUserResponse(user, true)
}

func (h *Handler) newPublicUserResponse(user *domain.User) *userResponse {
	return h.newUserResponse(user, false)
}

func (h *Handler) newUserResponse(user *domain.User, includeStatus bool) *userResponse {
	if user == nil {
		return nil
	}

	profile := h.newProfileResponse(user.Profile)
	response := &userResponse{
		ID:        user.ID,
		Email:     maskEmail(user.Email),
		CreatedAt: user.CreatedAt,
		UpdatedAt: user.UpdatedAt,
	}

	if includeStatus {
		status := user.StatusOrDefault()
		isActive := user.IsActive
		response.Status = &status
		response.IsActive = &isActive
	}

	if profile != nil {
		response.DisplayName = profile.DisplayName
		response.AvatarFileID = profile.AvatarFileID
		response.AvatarURL = profile.AvatarURL
		if includeStatus {
			response.FirstName = profile.FirstName
			response.LastName = profile.LastName
			response.BirthYear = profile.BirthYear
			response.Gender = profile.Gender
		}
	}

	return response
}

func (h *Handler) newProfileResponse(profile *domain.UserProfile) *profileResponse {
	profile = h.decorateProfile(profile)
	if profile == nil {
		return nil
	}

	return &profileResponse{
		ID:           profile.ID,
		UserID:       profile.UserID,
		DisplayName:  profile.DisplayName,
		FirstName:    profile.FirstName,
		LastName:     profile.LastName,
		BirthYear:    profile.BirthYear,
		Gender:       profile.Gender,
		AvatarFileID: profile.AvatarFileID,
		AvatarURL:    profile.AvatarURL,
		CreatedAt:    profile.CreatedAt,
		UpdatedAt:    profile.UpdatedAt,
	}
}

func (h *Handler) decorateProfile(profile *domain.UserProfile) *domain.UserProfile {
	if profile == nil || h.storage == nil {
		return profile
	}
	profile.WithAvatarURL(h.storage.DownloadURL)
	return profile
}

func maskEmail(email string) string {
	normalized := strings.TrimSpace(email)
	parts := strings.Split(normalized, "@")
	if len(parts) != 2 {
		return normalized
	}

	local, domain := parts[0], parts[1]
	localRunes := []rune(local)
	if len(localRunes) == 0 {
		return normalized
	}
	maskedLocal := string(localRunes[0]) + "****"

	domainName := domain
	domainSuffix := ""
	if dot := strings.LastIndex(domain, "."); dot > 0 && dot < len(domain)-1 {
		domainName = domain[:dot]
		domainSuffix = domain[dot:]
	}

	maskedDomain := "***"
	domainRunes := []rune(domainName)
	if len(domainRunes) > 0 {
		maskedDomain += string(domainRunes[len(domainRunes)-1])
	}

	return maskedLocal + "@" + maskedDomain + domainSuffix
}
