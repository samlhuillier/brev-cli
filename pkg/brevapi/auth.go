package brevapi

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	breverrors "github.com/brevdev/brev-cli/pkg/errors"
	"github.com/brevdev/brev-cli/pkg/files"
	"github.com/pkg/browser"
)

const (
	brevCredentialsFile    = "credentials.json"
	audiencePath           = "/api/v2/"
	waitThresholdInSeconds = 3
	// namespace used to set/get values from the keychain.
	SecNamespace = "auth0-cli"
)

var requiredScopes = []string{
	"openid",
	"offline_access", // <-- to get a refresh token.
	"create:clients", "delete:clients", "read:clients", "update:clients",
	"create:resource_servers", "delete:resource_servers", "read:resource_servers", "update:resource_servers",
	"create:roles", "delete:roles", "read:roles", "update:roles",
	"create:rules", "delete:rules", "read:rules", "update:rules",
	"create:users", "delete:users", "read:users", "update:users",
	"read:branding", "update:branding",
	"read:email_templates", "update:email_templates",
	"read:connections", "update:connections",
	"read:client_keys", "read:logs", "read:tenant_settings",
	"read:custom_domains", "create:custom_domains", "update:custom_domains", "delete:custom_domains",
	"read:anomaly_blocks", "delete:anomaly_blocks",
	"create:log_streams", "delete:log_streams", "read:log_streams", "update:log_streams",
	"create:actions", "delete:actions", "read:actions", "update:actions",
	"create:organizations", "delete:organizations", "read:organizations", "update:organizations",
}

type Authenticator struct {
	Audience           string
	ClientID           string
	DeviceCodeEndpoint string
	OauthTokenEndpoint string
}

// SecretStore provides access to stored sensitive data.
type SecretStore interface {
	// Get gets the secret
	Get(namespace, key string) (string, error)
	// Delete removes the secret
	Delete(namespace, key string) error
}

type Result struct {
	Tenant       string
	Domain       string
	RefreshToken string
	AccessToken  string
	IDToken      string
	ExpiresIn    int64
}

type State struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri_complete"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

type Credentials struct {
	AccessToken  string `json:"access_token"`
	ExpiresIn    int    `json:"expires_in"`
	IDToken      string `json:"id_token"`
	RefreshToken string `json:"refresh_token"`
}

type OauthToken struct {
	AccessToken  string `json:"access_token"`
	AuthMethod   string `json:"auth_method"`
	ExpiresIn    int    `json:"expires_in"`
	IDToken      string `json:"id_token"`
	RefreshToken string `json:"refresh_token"`
}

// RequiredScopes returns the scopes used for login.
func RequiredScopes() []string { return requiredScopes }

// RequiredScopesMin returns minimum scopes used for login in integration tests.
func RequiredScopesMin() []string {
	min := []string{}
	for _, s := range requiredScopes {
		if s != "offline_access" && s != "openid" {
			min = append(min, s)
		}
	}
	return min
}

func (s *State) IntervalDuration() time.Duration {
	return time.Duration(s.Interval+waitThresholdInSeconds) * time.Second
}

// Start kicks-off the device authentication flow
// by requesting a device code from Auth0,
// The returned state contains the URI for the next step of the flow.
func (a *Authenticator) Start(ctx context.Context) (State, error) {
	s, err := a.getDeviceCode(ctx)
	if err != nil {
		return State{}, breverrors.WrapAndTrace(err, "cannot get device code")
	}
	return s, nil
}

// Wait waits until the user is logged in on the browser.
func (a *Authenticator) Wait(ctx context.Context, state State) (Result, error) {
	t := time.NewTicker(state.IntervalDuration())
	for {
		select {
		case <-ctx.Done():
			return Result{}, breverrors.WrapAndTrace(ctx.Err())
		case <-t.C:
			data := url.Values{
				"client_id":   {a.ClientID},
				"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
				"device_code": {state.DeviceCode},
			}
			r, err := http.PostForm(a.OauthTokenEndpoint, data) //nolint:noctx // ignoring api call since planning to refactor api
			if err != nil {
				return Result{}, breverrors.WrapAndTrace(err, "cannot get device code")
			}

			var res struct {
				AccessToken      string  `json:"access_token"`
				IDToken          string  `json:"id_token"`
				RefreshToken     string  `json:"refresh_token"`
				Scope            string  `json:"scope"`
				ExpiresIn        int64   `json:"expires_in"`
				TokenType        string  `json:"token_type"`
				Error            *string `json:"error,omitempty"`
				ErrorDescription string  `json:"error_description,omitempty"`
			}

			err = json.NewDecoder(r.Body).Decode(&res)
			if err != nil {
				return Result{}, breverrors.WrapAndTrace(err, "cannot decode response")
			}

			if res.Error != nil {
				if *res.Error == "authorization_pending" {
					continue
				}
				return Result{}, breverrors.WrapAndTrace(errors.New(res.ErrorDescription))
			}

			ten, domain, err := parseTenant(res.AccessToken)
			if err != nil {
				return Result{}, breverrors.WrapAndTrace(err, "cannot parse tenant from the given access token")
			}

			if err = r.Body.Close(); err != nil {
				return Result{}, breverrors.WrapAndTrace(err)
			}
			return Result{
				RefreshToken: res.RefreshToken,
				AccessToken:  res.AccessToken,
				ExpiresIn:    res.ExpiresIn,
				Tenant:       ten,
				Domain:       domain,
				IDToken:      res.IDToken,
			}, nil
		}
	}
}

func (a *Authenticator) getDeviceCode(_ context.Context) (State, error) {
	data := url.Values{
		"client_id": {a.ClientID},
		"scope":     {strings.Join(requiredScopes, " ")},
		"audience":  {a.Audience},
	}
	r, err := http.PostForm(a.DeviceCodeEndpoint, data) //nolint:noctx // ignoring noctx since planning on refactoring api calls
	if err != nil {
		return State{}, breverrors.WrapAndTrace(err, "cannot get device code")
	}
	var res State
	err = json.NewDecoder(r.Body).Decode(&res)
	if err != nil {
		return State{}, breverrors.WrapAndTrace(err, "cannot decode response")
	}
	// TODO if status code > 399 handle errors
	// {"error":"unauthorized_client","error_description":"Grant type 'urn:ietf:params:oauth:grant-type:device_code' not allowed for the client.","error_uri":"https://auth0.com/docs/clients/client-grant-types"}

	if err = r.Body.Close(); err != nil {
		return State{}, breverrors.WrapAndTrace(err)
	}
	return res, nil
}

func parseTenant(accessToken string) (tenant, domain string, err error) {
	parts := strings.Split(accessToken, ".")
	v, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", "", breverrors.WrapAndTrace(err)
	}
	var payload struct {
		AUDs []string `json:"aud"`
	}
	if err := json.Unmarshal(v, &payload); err != nil {
		return "", "", breverrors.WrapAndTrace(err)
	}

	for _, aud := range payload.AUDs {
		u, err := url.Parse(aud)
		if err != nil {
			return "", "", breverrors.WrapAndTrace(err)
		}
		if u.Path == audiencePath {
			parts := strings.Split(u.Host, ".")
			return parts[0], u.Host, nil
		}
	}
	return "", "", breverrors.WrapAndTrace(fmt.Errorf("audience not found for %s", audiencePath))
}

// GetToken reads the previously-persisted token from the filesystem,
// returning nil for a token if it does not exist
func GetToken() (*OauthToken, error) {
	token, err := getTokenFromBrevConfigFile()
	if err != nil {
		return nil, breverrors.WrapAndTrace(err)
	}
	if token == nil { // we have not logged in yet
		err = Login(true)
		if err != nil {
			return nil, breverrors.WrapAndTrace(err)
		}
		// now that we have logged in, the file should contain the token
		token, err = getTokenFromBrevConfigFile()
		if err != nil {
			return nil, breverrors.WrapAndTrace(err)
		}
	}
	return token, nil
}

func getBrevCredentialsFile() (*string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, breverrors.WrapAndTrace(err)
	}
	brevCredentialsFile := home + "/" + files.GetBrevDirectory() + "/" + brevCredentialsFile
	return &brevCredentialsFile, nil
}

func WriteTokenToBrevConfigFile(token *Credentials) error {
	brevCredentialsFile, err := getBrevCredentialsFile()
	if err != nil {
		return breverrors.WrapAndTrace(err)
	}
	err = files.OverwriteJSON(*brevCredentialsFile, token)
	if err != nil {
		return breverrors.WrapAndTrace(err)
	}

	return nil
}

func getTokenFromBrevConfigFile() (*OauthToken, error) {
	brevCredentialsFile, err := getBrevCredentialsFile()
	if err != nil {
		return nil, breverrors.WrapAndTrace(err)
	}

	exists, err := files.Exists(*brevCredentialsFile, false)
	if err != nil {
		return nil, breverrors.WrapAndTrace(err)
	}
	if !exists {
		return nil, &breverrors.CredentialsFileNotFound{}
	}

	var token OauthToken
	err = files.ReadJSON(files.AppFs, *brevCredentialsFile, &token)
	if err != nil {
		return nil, breverrors.WrapAndTrace(err)
	}

	return &token, nil
}

func Login(prompt bool) error {
	if prompt {
		reader := bufio.NewReader(os.Stdin)
		fmt.Print(`You are currently logged out, would you like to log in? [y/n]: `)
		text, _ := reader.ReadString('\n')
		if strings.Compare(text, "y") != 1 {
			return &breverrors.DeclineToLoginError{}
		}
	}
	ctx := context.Background()

	// TODO env vars
	authenticator := Authenticator{
		Audience:           "https://brevdev.us.auth0.com/api/v2/",
		ClientID:           "JaqJRLEsdat5w7Tb0WqmTxzIeqwqepmk",
		DeviceCodeEndpoint: "https://brevdev.us.auth0.com/oauth/device/code",
		OauthTokenEndpoint: "https://brevdev.us.auth0.com/oauth/token",
	}
	state, err := authenticator.Start(ctx)
	if err != nil {
		return breverrors.WrapAndTrace(err, "could not start the authentication process")
	}

	// todo color library
	// fmt.Printf("Your Device Confirmation code is: %s\n\n", ansi.Bold(state.UserCode))
	// cli.renderer.Infof("%s to open the browser to log in or %s to quit...", ansi.Green("Press Enter"), ansi.Red("^C"))
	// fmt.Scanln()
	// TODO make this stand out! its important
	fmt.Println("Your Device Confirmation Code is", state.UserCode)

	err = browser.OpenURL(state.VerificationURI)

	if err != nil {
		fmt.Println("please open: ", state.VerificationURI)
	}

	fmt.Println("waiting for auth to complete")
	var res Result

	res, err = authenticator.Wait(ctx, state)

	if err != nil {
		return breverrors.WrapAndTrace(err, "login error")
	}

	fmt.Print("\n")
	fmt.Println("Successfully logged in.")
	creds := &Credentials{
		AccessToken:  res.AccessToken,
		RefreshToken: res.RefreshToken,
		ExpiresIn:    int(res.ExpiresIn),
		IDToken:      res.IDToken,
	}
	// store the refresh token
	err = WriteTokenToBrevConfigFile(creds)
	if err != nil {
		return breverrors.WrapAndTrace(err)
	}

	// hydrate the cache
	_, _, err = WriteCaches()
	if err != nil {
		return breverrors.WrapAndTrace(err)
	}

	return nil
}

func Logout() error {
	brevCredentialsFile, err := getBrevCredentialsFile()
	if err != nil {
		return breverrors.WrapAndTrace(err)
	}
	err = files.DeleteFile(*brevCredentialsFile)
	if err != nil {
		return breverrors.WrapAndTrace(err)
	}
	return nil
}
