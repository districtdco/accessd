package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/districtd/pam/api/internal/access"
	"github.com/districtd/pam/api/internal/admin"
	"github.com/districtd/pam/api/internal/assets"
	"github.com/districtd/pam/api/internal/auth"
	"github.com/districtd/pam/api/internal/config"
	"github.com/districtd/pam/api/internal/credentials"
	"github.com/districtd/pam/api/internal/handlers"
	"github.com/districtd/pam/api/internal/httpserver"
	"github.com/districtd/pam/api/internal/migrate"
	"github.com/districtd/pam/api/internal/sessions"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

var (
	testPool     *pgxpool.Pool
	testPoolErr  error
	testPoolOnce sync.Once
)

func TestMain(m *testing.M) {
	code := m.Run()
	if testPool != nil {
		testPool.Close()
	}
	os.Exit(code)
}

type testHarness struct {
	t        *testing.T
	ctx      context.Context
	pool     *pgxpool.Pool
	router   http.Handler
	authSvc  *auth.Service
	assets   *assets.Service
	access   *access.Service
	seed     seedData
	baseTime time.Time
}

type seedData struct {
	adminID         string
	adminUsername   string
	adminPassword   string
	operatorID      string
	operatorName    string
	operatorPass    string
	viewerID        string
	viewerName      string
	viewerPass      string
	allowedAssetID  string
	deniedAssetID   string
	secondaryAsset  string
	secondaryUserID string
}

func newTestHarness(t *testing.T) *testHarness {
	t.Helper()

	ctx := context.Background()
	pool := getTestPool(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	h := &testHarness{
		t:        t,
		ctx:      ctx,
		pool:     pool,
		baseTime: time.Date(2026, time.April, 1, 10, 0, 0, 0, time.UTC),
	}

	h.resetDB()
	h.authSvc = h.newAuthService(logger)
	if err := h.authSvc.Bootstrap(ctx); err != nil {
		t.Fatalf("bootstrap auth: %v", err)
	}

	h.assets = assets.NewService(pool, logger)
	h.access = access.NewService(pool, logger)

	h.seed = h.seedData(logger)
	h.router = h.newRouter(logger)
	return h
}

func (h *testHarness) newAuthService(logger *slog.Logger) *auth.Service {
	h.t.Helper()
	svc, err := auth.NewService(h.pool, config.AuthConfig{
		SessionCookieName: "pam_test_session",
		SessionTTL:        2 * time.Hour,
		SessionSecure:     false,
		DevAdminUsername:  "admin",
		DevAdminPassword:  "admin123",
		DevAdminEmail:     "admin@pam.local",
		DevAdminName:      "PAM Admin",
		ProviderMode:      "local",
	}, logger)
	if err != nil {
		h.t.Fatalf("new auth service: %v", err)
	}
	return svc
}

func (h *testHarness) newRouter(logger *slog.Logger) http.Handler {
	h.t.Helper()
	adminSvc := admin.NewService(h.pool, logger, config.AuthConfig{
		ProviderMode: "local",
		LDAP:         config.LDAPConfig{},
	})
	cipher, err := credentials.NewCipher("pam-test-vault-key", "v1")
	if err != nil {
		h.t.Fatalf("new cipher: %v", err)
	}
	credentialsSvc := credentials.NewService(h.pool, cipher, logger)
	sessionsSvc, err := sessions.NewService(h.pool, sessions.Config{
		LaunchTokenSecret: []byte("pam-test-launch-secret"),
		LaunchTokenTTL:    5 * time.Minute,
		ConnectorSecret:   []byte("pam-test-connector-secret"),
		ProxyHost:         "127.0.0.1",
		ProxyPort:         2222,
		ProxyUsername:     "pam",
	}, logger)
	if err != nil {
		h.t.Fatalf("new sessions service: %v", err)
	}

	return httpserver.NewRouter(httpserver.RouteHandlers{
		Health:   handlers.NewHealthHandler(h.pool),
		Version:  handlers.NewVersionHandler(config.VersionInfo{Service: "pam-api", Version: "test", Commit: "test", BuiltAt: "test"}),
		Auth:     handlers.NewAuthHandler(h.authSvc),
		Access:   handlers.NewAccessHandler(h.access),
		Sessions: handlers.NewSessionsHandler(h.assets, h.access, credentialsSvc, sessionsSvc, nil, nil, nil, nil),
		Admin:    handlers.NewAdminHandler(adminSvc, h.assets, credentialsSvc),
		AuthSvc:  h.authSvc,
	})
}

func (h *testHarness) seedData(_ *slog.Logger) seedData {
	h.t.Helper()

	adminID := h.mustUserIDByUsername("admin")
	operatorID := h.createLocalUserWithRole("operator1", "operator123", "operator@example.com", "Operator One", "user")
	viewerID := h.createLocalUserWithRole("viewer1", "viewer123", "viewer@example.com", "Viewer One", "user")
	secondaryUserID := h.createLocalUserWithRole("operator2", "operator456", "operator2@example.com", "Operator Two", "user")

	createdBy := &adminID
	allowedAsset, err := h.assets.Create(h.ctx, assets.CreateInput{
		Name:      "linux-allowed",
		Type:      assets.TypeLinuxVM,
		Host:      "10.10.1.10",
		Port:      22,
		CreatedBy: createdBy,
	})
	if err != nil {
		h.t.Fatalf("create allowed asset: %v", err)
	}
	deniedAsset, err := h.assets.Create(h.ctx, assets.CreateInput{
		Name:      "linux-denied",
		Type:      assets.TypeLinuxVM,
		Host:      "10.10.1.11",
		Port:      22,
		CreatedBy: createdBy,
	})
	if err != nil {
		h.t.Fatalf("create denied asset: %v", err)
	}
	secondaryAsset, err := h.assets.Create(h.ctx, assets.CreateInput{
		Name:      "linux-secondary",
		Type:      assets.TypeLinuxVM,
		Host:      "10.10.1.12",
		Port:      22,
		CreatedBy: createdBy,
	})
	if err != nil {
		h.t.Fatalf("create secondary asset: %v", err)
	}

	if err := h.access.GrantUserAction(h.ctx, operatorID, allowedAsset.ID, access.ActionShell, createdBy); err != nil {
		h.t.Fatalf("grant operator access: %v", err)
	}
	if err := h.access.GrantUserAction(h.ctx, secondaryUserID, secondaryAsset.ID, access.ActionShell, createdBy); err != nil {
		h.t.Fatalf("grant secondary operator access: %v", err)
	}

	return seedData{
		adminID:         adminID,
		adminUsername:   "admin",
		adminPassword:   "admin123",
		operatorID:      operatorID,
		operatorName:    "operator1",
		operatorPass:    "operator123",
		viewerID:        viewerID,
		viewerName:      "viewer1",
		viewerPass:      "viewer123",
		allowedAssetID:  allowedAsset.ID,
		deniedAssetID:   deniedAsset.ID,
		secondaryAsset:  secondaryAsset.ID,
		secondaryUserID: secondaryUserID,
	}
}

func (h *testHarness) createLocalUserWithRole(username, password, email, displayName, role string) string {
	h.t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		h.t.Fatalf("hash password for %s: %v", username, err)
	}

	const userInsert = `
INSERT INTO users (username, email, display_name, is_active, auth_provider, password_hash)
VALUES ($1, $2, $3, TRUE, 'local', $4)
RETURNING id;`
	var userID string
	if err := h.pool.QueryRow(h.ctx, userInsert, username, email, displayName, string(hash)).Scan(&userID); err != nil {
		h.t.Fatalf("insert user %s: %v", username, err)
	}

	const roleAssign = `
INSERT INTO user_roles (user_id, role_id)
SELECT $1, id FROM roles WHERE name = $2
ON CONFLICT (user_id, role_id) DO NOTHING;`
	if _, err := h.pool.Exec(h.ctx, roleAssign, userID, role); err != nil {
		h.t.Fatalf("assign role %s to %s: %v", role, username, err)
	}

	return userID
}

func (h *testHarness) mustUserIDByUsername(username string) string {
	h.t.Helper()
	const query = `SELECT id FROM users WHERE username = $1 LIMIT 1;`
	var id string
	if err := h.pool.QueryRow(h.ctx, query, username).Scan(&id); err != nil {
		h.t.Fatalf("find user %s: %v", username, err)
	}
	return id
}

func (h *testHarness) resetDB() {
	h.t.Helper()
	const truncate = `
TRUNCATE TABLE
	auth_sessions,
	user_roles,
	roles,
	access_grants,
	credentials,
	asset_protocols,
	user_groups,
	groups,
	session_events,
	audit_events,
	sessions,
	assets,
	users
RESTART IDENTITY CASCADE;`
	if _, err := h.pool.Exec(h.ctx, truncate); err != nil {
		h.t.Fatalf("truncate database: %v", err)
	}
}

func (h *testHarness) login(username, password string) *http.Cookie {
	h.t.Helper()
	resp := h.requestJSON(http.MethodPost, "/auth/login", map[string]any{"username": username, "password": password}, nil)
	if resp.Code != http.StatusOK {
		h.t.Fatalf("login failed for %s: expected 200 got %d body=%s", username, resp.Code, resp.Body.String())
	}
	cookies := resp.Result().Cookies()
	if len(cookies) == 0 {
		h.t.Fatalf("login did not set cookie for %s", username)
	}
	return cookies[0]
}

func (h *testHarness) requestJSON(method, path string, body any, cookie *http.Cookie) *httptest.ResponseRecorder {
	h.t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			h.t.Fatalf("marshal request body: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, reader)
	req.RemoteAddr = "127.0.0.1:55444"
	req.Header.Set("User-Agent", "integration-test")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}

	rr := httptest.NewRecorder()
	h.router.ServeHTTP(rr, req)
	return rr
}

func (h *testHarness) responseJSON(t *testing.T, rr *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	if rr.Body.Len() == 0 {
		return map[string]any{}
	}
	var payload map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode json response: %v, body=%s", err, rr.Body.String())
	}
	return payload
}

func getTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := strings.TrimSpace(os.Getenv("PAM_TEST_DB_URL"))
	if url == "" {
		t.Skip("set PAM_TEST_DB_URL to run backend integration tests")
	}
	if !strings.Contains(strings.ToLower(url), "test") && os.Getenv("PAM_TEST_DB_UNSAFE_OK") != "1" {
		t.Skip("PAM_TEST_DB_URL must point to a dedicated test database (or set PAM_TEST_DB_UNSAFE_OK=1)")
	}

	testPoolOnce.Do(func() {
		ctx := context.Background()
		pool, err := pgxpool.New(ctx, url)
		if err != nil {
			testPoolErr = fmt.Errorf("open test db pool: %w", err)
			return
		}

		logger := slog.New(slog.NewTextHandler(io.Discard, nil))
		runner := migrate.NewRunner(pool, config.MigrationConfig{
			Dir:   filepath.Join(apiModuleRoot(), "migrations"),
			Table: "schema_migrations",
		}, logger)
		if err := runner.Up(ctx); err != nil {
			pool.Close()
			testPoolErr = fmt.Errorf("apply migrations: %w", err)
			return
		}
		testPool = pool
	})

	if testPoolErr != nil {
		t.Fatalf("initialize test pool: %v", testPoolErr)
	}
	return testPool
}

func apiModuleRoot() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		panic("failed to resolve caller path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}
