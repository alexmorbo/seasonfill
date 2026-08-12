package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	admin "github.com/alexmorbo/seasonfill/internal/admin/domain"
	jellyfin "github.com/alexmorbo/seasonfill/internal/shared/clients/jellyfin"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
)

// fakeAuthr records the (username,password) it receives and returns a canned
// identity/error — proving what the usecase forwards to the authenticator.
type fakeAuthr struct {
	user             jellyfin.JellyfinUser
	err              error
	gotUser, gotPass string
}

func (f *fakeAuthr) AuthenticateByName(_ context.Context, u, p string) (jellyfin.JellyfinUser, error) {
	f.gotUser, f.gotPass = u, p
	return f.user, f.err
}

// jfCall captures one recorded repo invocation so the test can assert the
// password never reaches the repository.
type jfCall struct {
	method string
	args   []string
}

// fakeJFRepo is a minimal ports.UserRepository double that records every call
// it receives and can be seeded with an existing jellyfin user.
type fakeJFRepo struct {
	seeded    *admin.User // when set, GetByJellyfinUserID returns it (hit branch)
	created   []admin.User
	createCnt int
	calls     []jfCall
}

func (r *fakeJFRepo) record(method string, args ...string) {
	r.calls = append(r.calls, jfCall{method: method, args: args})
}

func (r *fakeJFRepo) GetByJellyfinUserID(_ context.Context, jellyfinUserID string) (admin.User, error) {
	r.record("GetByJellyfinUserID", jellyfinUserID)
	if r.seeded != nil {
		return *r.seeded, nil
	}
	return admin.User{}, errors.Join(&sharedErrors.UserNotFoundError{}, ports.ErrNotFound)
}

func (r *fakeJFRepo) CreateFromJellyfin(_ context.Context, jellyfinUserID, username, email string) (admin.User, error) {
	r.record("CreateFromJellyfin", jellyfinUserID, username, email)
	r.createCnt++
	jid := jellyfinUserID
	u := admin.User{
		Username:       username,
		JellyfinUserID: &jid,
		Role:           admin.RoleUser,
		AvatarMode:     admin.AvatarModeAuto,
		Request:        true,
	}
	if email != "" {
		e := email
		u.Email = &e
	}
	r.created = append(r.created, u)
	return u, nil
}

func (r *fakeJFRepo) UpdateLastLoginAt(_ context.Context, userID uint, _ time.Time) error {
	r.record("UpdateLastLoginAt")
	_ = userID
	return nil
}

// Unused-by-usecase interface methods.
func (r *fakeJFRepo) Get(context.Context) (admin.User, error) {
	return admin.User{}, ports.ErrNotFound
}
func (r *fakeJFRepo) GetByUsername(context.Context, string) (admin.User, error) {
	return admin.User{}, ports.ErrNotFound
}
func (r *fakeJFRepo) FirstAdminID(context.Context) (int64, error) {
	return 0, ports.ErrNotFound
}
func (r *fakeJFRepo) GetByOIDCSubject(context.Context, string) (admin.User, error) {
	return admin.User{}, ports.ErrNotFound
}
func (r *fakeJFRepo) Create(context.Context, admin.User) error { return nil }
func (r *fakeJFRepo) CreateFromOIDC(context.Context, string, string, string) (admin.User, error) {
	return admin.User{}, nil
}
func (r *fakeJFRepo) UpdatePassword(context.Context, uint, string) error { return nil }
func (r *fakeJFRepo) UpdateSettings(context.Context, uint, ports.UserSettingsPatch) error {
	return nil
}

// assertPasswordNeverRecorded fails if the secret appears in any recorded arg.
func assertPasswordNeverRecorded(t *testing.T, r *fakeJFRepo, password string) {
	t.Helper()
	for _, c := range r.calls {
		for _, a := range c.args {
			if a == password {
				t.Fatalf("password %q leaked into repo call %s", password, c.method)
			}
		}
	}
}

func TestJellyfinLogin_FirstSeen_CreatesRequester(t *testing.T) {
	authr := &fakeAuthr{user: jellyfin.JellyfinUser{ID: "jf-1", Name: "alice"}}
	repo := &fakeJFRepo{}
	uc := NewJellyfinLoginUseCase(repo)

	got, err := uc.Login(context.Background(), authr, "alice", "s3cret")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if authr.gotUser != "alice" || authr.gotPass != "s3cret" {
		t.Fatalf("authenticator got (%q,%q), want (alice,s3cret)", authr.gotUser, authr.gotPass)
	}
	if repo.createCnt != 1 {
		t.Fatalf("createCnt = %d, want 1", repo.createCnt)
	}
	c := repo.created[0]
	if c.Role != admin.RoleUser {
		t.Errorf("role = %q, want user", c.Role)
	}
	if !c.Request {
		t.Errorf("Request = false, want true")
	}
	if c.AutoApprove || c.ManageRequests || c.ManageUsers || c.Request4K {
		t.Errorf("perms not default-false: %+v", c)
	}
	if c.JellyfinUserID == nil || *c.JellyfinUserID != "jf-1" {
		t.Errorf("JellyfinUserID = %v, want jf-1", c.JellyfinUserID)
	}
	if c.PasswordHash != "" {
		t.Errorf("PasswordHash = %q, want empty", c.PasswordHash)
	}
	if got.Username != "alice" {
		t.Errorf("returned username = %q, want alice", got.Username)
	}
	// CreateFromJellyfin got (jfID, jfName, "").
	var createArgs []string
	for _, cc := range repo.calls {
		if cc.method == "CreateFromJellyfin" {
			createArgs = cc.args
		}
	}
	if len(createArgs) != 3 || createArgs[0] != "jf-1" || createArgs[1] != "alice" || createArgs[2] != "" {
		t.Errorf("CreateFromJellyfin args = %v, want [jf-1 alice \"\"]", createArgs)
	}
	assertPasswordNeverRecorded(t, repo, "s3cret")
}

func TestJellyfinLogin_ExistingUser_NoCreate(t *testing.T) {
	jid := "jf-7"
	seeded := admin.User{ID: 5, Username: "bob", Role: admin.RoleUser, JellyfinUserID: &jid, Request: true}
	authr := &fakeAuthr{user: jellyfin.JellyfinUser{ID: "jf-7", Name: "bob"}}
	repo := &fakeJFRepo{seeded: &seeded}
	uc := NewJellyfinLoginUseCase(repo)

	got, err := uc.Login(context.Background(), authr, "bob", "pw")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if repo.createCnt != 0 {
		t.Fatalf("createCnt = %d, want 0 (existing user)", repo.createCnt)
	}
	if got.ID != 5 {
		t.Errorf("returned id = %d, want 5", got.ID)
	}
	assertPasswordNeverRecorded(t, repo, "pw")
}

func TestJellyfinLogin_InvalidCreds_MapsToLoginFailed(t *testing.T) {
	authr := &fakeAuthr{err: jellyfin.ErrJellyfinAuthFailed}
	repo := &fakeJFRepo{}
	uc := NewJellyfinLoginUseCase(repo)

	_, err := uc.Login(context.Background(), authr, "eve", "wrong")
	if !errors.Is(err, ErrJellyfinLoginFailed) {
		t.Fatalf("err = %v, want ErrJellyfinLoginFailed", err)
	}
	if repo.createCnt != 0 || len(repo.calls) != 0 {
		t.Fatalf("repo touched on failed auth: createCnt=%d calls=%v", repo.createCnt, repo.calls)
	}
}

func TestJellyfinLogin_PasswordChange_Rejected(t *testing.T) {
	// Old password no longer valid at Jellyfin → per-login validation fails.
	authr := &fakeAuthr{err: jellyfin.ErrJellyfinAuthFailed}
	repo := &fakeJFRepo{}
	uc := NewJellyfinLoginUseCase(repo)

	_, err := uc.Login(context.Background(), authr, "carol", "old-pw")
	if !errors.Is(err, ErrJellyfinLoginFailed) {
		t.Fatalf("err = %v, want ErrJellyfinLoginFailed", err)
	}
}
