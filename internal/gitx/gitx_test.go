package gitx

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

// initBareWithMainHEAD creates a bare repo with HEAD pointing at
// refs/heads/main so Clone resolves after our first push. Mirrors
// the helper in sync_test.go and harness.NewScenario — gitx.Init
// pushes to main (DefaultBranch) but go-git's PlainInit leaves the
// bare's HEAD at master, so we explicitly align.
func initBareWithMainHEAD(t *testing.T, path string) {
	t.Helper()
	r, err := git.PlainInit(path, true)
	if err != nil {
		t.Fatal(err)
	}
	ref := plumbing.NewSymbolicReference(plumbing.HEAD, plumbing.NewBranchReferenceName(DefaultBranch))
	if err := r.Storer.SetReference(ref); err != nil {
		t.Fatal(err)
	}
}

func TestLocalRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	bareDir := filepath.Join(tmp, "bare.git")
	wt1Dir := filepath.Join(tmp, "wt1")
	wt2Dir := filepath.Join(tmp, "wt2")

	initBareWithMainHEAD(t, bareDir)

	ctx := context.Background()

	wt1, err := Init(wt1Dir, bareDir)
	if err != nil {
		t.Fatalf("init wt1: %v", err)
	}

	empty, err := wt1.IsEmpty()
	if err != nil {
		t.Fatal(err)
	}
	if !empty {
		t.Fatal("fresh init should be empty")
	}

	if err := os.WriteFile(filepath.Join(wt1Dir, "hello.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := wt1.AddAll(); err != nil {
		t.Fatal(err)
	}
	hash, err := wt1.Commit("init", "Test", "test@example.com")
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if hash == "" {
		t.Fatal("commit returned empty hash")
	}

	empty, err = wt1.IsEmpty()
	if err != nil {
		t.Fatal(err)
	}
	if empty {
		t.Fatal("after commit should not be empty")
	}

	headSHA, err := wt1.HeadSHA()
	if err != nil {
		t.Fatal(err)
	}
	if headSHA != hash {
		t.Errorf("HeadSHA %q != commit hash %q", headSHA, hash)
	}

	if err := wt1.Push(ctx, nil); err != nil {
		t.Fatalf("push: %v", err)
	}

	wt2, err := Clone(ctx, bareDir, wt2Dir, nil)
	if err != nil {
		t.Fatalf("clone wt2: %v", err)
	}

	data, ok, err := wt2.BlobAtHead("hello.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("BlobAtHead: file not found after clone")
	}
	if string(data) != "hi" {
		t.Errorf("blob contents = %q, want %q", data, "hi")
	}

	missing, ok, err := wt2.BlobAtHead("no-such-file.txt")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Errorf("BlobAtHead for missing file returned ok=true, data=%q", missing)
	}

	log, err := wt2.Log(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(log) != 1 || log[0].SHA != hash {
		t.Errorf("Log mismatch: %+v", log)
	}
}

func TestPullFastForward(t *testing.T) {
	tmp := t.TempDir()
	bareDir := filepath.Join(tmp, "bare.git")
	wt1Dir := filepath.Join(tmp, "wt1")
	wt2Dir := filepath.Join(tmp, "wt2")

	initBareWithMainHEAD(t, bareDir)

	ctx := context.Background()

	wt1, _ := Init(wt1Dir, bareDir)
	if err := os.WriteFile(filepath.Join(wt1Dir, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	wt1.AddAll()
	wt1.Commit("a", "t", "t@e")
	wt1.Push(ctx, nil)

	wt2, err := Clone(ctx, bareDir, wt2Dir, nil)
	if err != nil {
		t.Fatalf("clone wt2: %v", err)
	}

	// wt1 makes another commit and pushes
	if err := os.WriteFile(filepath.Join(wt1Dir, "b.txt"), []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}
	wt1.AddAll()
	wt1.Commit("b", "t", "t@e")
	if err := wt1.Push(ctx, nil); err != nil {
		t.Fatalf("push b: %v", err)
	}

	// wt2 pulls and should see b.txt
	if err := wt2.Pull(ctx, nil); err != nil {
		t.Fatalf("pull: %v", err)
	}
	data, ok, err := wt2.BlobAtHead("b.txt")
	if err != nil || !ok {
		t.Fatalf("after pull, b.txt ok=%v err=%v", ok, err)
	}
	if string(data) != "b" {
		t.Errorf("b.txt content = %q", data)
	}
}

func TestAuthConfigResolveNone(t *testing.T) {
	a, err := AuthConfig{Kind: AuthNone}.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if a != nil {
		t.Errorf("AuthNone should resolve to nil, got %v", a)
	}
}

func TestAuthConfigHTTPSRequiresToken(t *testing.T) {
	_, err := AuthConfig{Kind: AuthHTTPS}.Resolve()
	if err == nil {
		t.Fatal("expected error without token")
	}
}

func TestAuthConfigHTTPSWithToken(t *testing.T) {
	a, err := AuthConfig{Kind: AuthHTTPS, HTTPSToken: "tok"}.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if a == nil {
		t.Fatal("expected non-nil auth")
	}
}
