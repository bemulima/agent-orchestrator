package usecase

import (
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/example/auth-service/config"
	"github.com/example/auth-service/internal/tokenverify"
)

type Tokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	ReturnTo     string `json:"return_to,omitempty"`
}

// VerificationResult is returned by token verification routines.
type VerificationResult = tokenverify.Result

type JWTSigner interface {
	SignAccessToken(subject string, claims map[string]interface{}, ttl time.Duration) (string, error)
	SignRefreshToken(subject, jti string, ttl time.Duration) (string, error)
	Parse(token string) (*jwt.Token, jwt.MapClaims, error)
}

type jwtSigner struct {
	cfg       *config.Config
	hmacKey   []byte
	private   *rsa.PrivateKey
	publicKey *rsa.PublicKey
}

func NewJWTSigner(cfg *config.Config) (JWTSigner, error) {
	s := &jwtSigner{cfg: cfg}
	if cfg.JWTSecret != "" {
		s.hmacKey = []byte(cfg.JWTSecret)
		return s, nil
	}
	if cfg.JWTPrivateKey != "" && cfg.JWTPublicKey != "" {
		priv, err := jwt.ParseRSAPrivateKeyFromPEM([]byte(cfg.JWTPrivateKey))
		if err != nil {
			return nil, err
		}
		pub, err := jwt.ParseRSAPublicKeyFromPEM([]byte(cfg.JWTPublicKey))
		if err != nil {
			return nil, err
		}
		s.private = priv
		s.publicKey = pub
		return s, nil
	}
	return nil, errors.New("jwt secret or key pair required")
}

func (s *jwtSigner) SignAccessToken(subject string, claims map[string]interface{}, ttl time.Duration) (string, error) {
	token := jwt.New(jwt.GetSigningMethod(s.method()))
	now := time.Now().UTC()
	std := token.Claims.(jwt.MapClaims)
	std["sub"] = subject
	std["iss"] = s.cfg.JWTIssuer
	std["aud"] = s.cfg.JWTAudience
	std["exp"] = now.Add(ttl).Unix()
	std["iat"] = now.Unix()
	for k, v := range claims {
		std[k] = v
	}
	return s.sign(token)
}

func (s *jwtSigner) SignRefreshToken(subject, jti string, ttl time.Duration) (string, error) {
	token := jwt.New(jwt.GetSigningMethod(s.method()))
	now := time.Now().UTC()
	std := token.Claims.(jwt.MapClaims)
	std["sub"] = subject
	std["jti"] = jti
	std["typ"] = "refresh"
	std["iss"] = s.cfg.JWTIssuer
	std["aud"] = s.cfg.JWTAudience
	std["exp"] = now.Add(ttl).Unix()
	std["iat"] = now.Unix()
	return s.sign(token)
}

func (s *jwtSigner) Parse(tokenStr string) (*jwt.Token, jwt.MapClaims, error) {
	claims := jwt.MapClaims{}
	parser := jwt.NewParser(jwt.WithAudience(s.cfg.JWTAudience), jwt.WithIssuer(s.cfg.JWTIssuer), jwt.WithLeeway(30*time.Second))
	token, err := parser.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (interface{}, error) {
		if s.hmacKey != nil {
			return s.hmacKey, nil
		}
		return s.publicKey, nil
	})
	return token, claims, err
}

func (s *jwtSigner) sign(token *jwt.Token) (string, error) {
	if s.hmacKey != nil {
		return token.SignedString(s.hmacKey)
	}
	if s.private == nil {
		return "", errors.New("private key not configured")
	}
	return token.SignedString(s.private)
}

func (s *jwtSigner) method() string {
	if s.hmacKey != nil {
		return jwt.SigningMethodHS256.Alg()
	}
	return jwt.SigningMethodRS256.Alg()
}

func GenerateJTI() (string, error) {
	uuid := make([]byte, 16)
	if _, err := rand.Read(uuid); err != nil {
		return "", err
	}
	uuid[6] = (uuid[6] & 0x0f) | 0x40
	uuid[8] = (uuid[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", uuid[0:4], uuid[4:6], uuid[6:8], uuid[8:10], uuid[10:]), nil
}
