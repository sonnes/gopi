package chatgpt

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"slices"
	"strings"
	"time"
)

// signingAlg is the only signing algorithm of the ID tokens of OpenAI,
// as id_token_signing_alg_values_supported of its OpenID configuration
// gives it. RS256 needs only crypto/rsa, so the package has no JOSE
// dependency.
const signingAlg = "RS256"

// idClaims are the claims of an ID token that a sign-in reads.
type idClaims struct {
	Issuer   string   `json:"iss"`
	Audience audience `json:"aud"`
	Expiry   int64    `json:"exp"`
	Nonce    string   `json:"nonce"`
	Subject  string   `json:"sub"`
	Email    string   `json:"email"`
}

// audience is the "aud" claim. It is one string or a list of strings.
type audience []string

func (a *audience) UnmarshalJSON(data []byte) error {
	var one string
	if err := json.Unmarshal(data, &one); err == nil {
		*a = audience{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(data, &many); err != nil {
		return err
	}
	*a = many
	return nil
}

// verifyIDToken checks the signature of raw against the keys at the
// JWKS URL, then its issuer, its audience, its expiry, and its nonce.
func (a Auth) verifyIDToken(ctx context.Context, raw, clientID, nonce string) (idClaims, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return idClaims{}, errors.New("the ID token is not a JWT")
	}
	enc := base64.RawURLEncoding
	headerJSON, err := enc.DecodeString(parts[0])
	if err != nil {
		return idClaims{}, fmt.Errorf("read the header of the ID token: %w", err)
	}
	var header struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		return idClaims{}, fmt.Errorf("read the header of the ID token: %w", err)
	}
	if header.Alg != signingAlg {
		return idClaims{}, fmt.Errorf("the ID token uses %q, not %s", header.Alg, signingAlg)
	}

	key, err := a.signingKey(ctx, header.Kid)
	if err != nil {
		return idClaims{}, err
	}
	sig, err := enc.DecodeString(parts[2])
	if err != nil {
		return idClaims{}, fmt.Errorf("read the signature of the ID token: %w", err)
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, sum[:], sig); err != nil {
		return idClaims{}, errors.New("the signature of the ID token is not valid")
	}

	claimsJSON, err := enc.DecodeString(parts[1])
	if err != nil {
		return idClaims{}, fmt.Errorf("read the claims of the ID token: %w", err)
	}
	var c idClaims
	if err := json.Unmarshal(claimsJSON, &c); err != nil {
		return idClaims{}, fmt.Errorf("read the claims of the ID token: %w", err)
	}
	switch {
	case c.Issuer != a.issuer():
		return idClaims{}, fmt.Errorf("the ID token has the issuer %q", c.Issuer)
	case !slices.Contains(c.Audience, clientID):
		return idClaims{}, errors.New("the ID token is for another client_id")
	case time.Now().After(time.Unix(c.Expiry, 0)):
		return idClaims{}, errors.New("the ID token expired")
	case c.Nonce != nonce:
		return idClaims{}, errors.New("the ID token has another nonce")
	}
	return c, nil
}

// signingKey gets the RSA key with the key ID kid from the JWKS URL.
func (a Auth) signingKey(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.jwksURL(), nil)
	if err != nil {
		return nil, err
	}
	res, err := a.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("get the signing keys: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get the signing keys: %s", res.Status)
	}
	var set struct {
		Keys []struct {
			Kty string `json:"kty"`
			Kid string `json:"kid"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(res.Body).Decode(&set); err != nil {
		return nil, fmt.Errorf("read the signing keys: %w", err)
	}
	for _, k := range set.Keys {
		if k.Kty != "RSA" || k.Kid != kid {
			continue
		}
		n, errN := base64.RawURLEncoding.DecodeString(k.N)
		e, errE := base64.RawURLEncoding.DecodeString(k.E)
		if errN != nil || errE != nil {
			return nil, fmt.Errorf("read the signing key %q", kid)
		}
		return &rsa.PublicKey{
			N: new(big.Int).SetBytes(n),
			E: int(new(big.Int).SetBytes(e).Int64()),
		}, nil
	}
	return nil, fmt.Errorf("no signing key %q", kid)
}
