package hostrunner

import (
	"context"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/termipod/hub/internal/envseal"
)

func TestSecretKVList_SortedAndFormatted(t *testing.T) {
	got := secretKVList(map[string]string{"B": "2", "A": "1", "C": ""})
	want := []string{"A=1", "B=2", "C="}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("secretKVList = %v, want %v", got, want)
	}
	if secretKVList(nil) != nil {
		t.Fatal("nil map should yield nil")
	}
}

func TestSecretKeyNames(t *testing.T) {
	got := secretKeyNames([]string{"OPENAI_API_KEY=sk-x", "DB=postgres://p"})
	want := []string{"OPENAI_API_KEY", "DB"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("secretKeyNames = %v, want %v", got, want)
	}
}

func TestNewWindowArgs_EnvBeforeCmd(t *testing.T) {
	args := newWindowArgs("hub-agents", "w1", "claude --yolo", []string{"A=1", "B=2"})
	// cmd must be the final positional; each secret rides its own -e.
	if args[len(args)-1] != "claude --yolo" {
		t.Fatalf("cmd not last: %v", args)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-e A=1") || !strings.Contains(joined, "-e B=2") {
		t.Fatalf("missing -e entries: %v", args)
	}
	// No env → no -e flags, cmd still last.
	plain := newWindowArgs("hub-agents", "w1", "claude", nil)
	for _, a := range plain {
		if a == "-e" {
			t.Fatalf("unexpected -e with no env: %v", plain)
		}
	}
	if plain[len(plain)-1] != "claude" {
		t.Fatalf("cmd not last: %v", plain)
	}
}

// noEnvLauncher implements Launcher but NOT envLauncher.
type noEnvLauncher struct{ lastCmd string }

func (l *noEnvLauncher) Launch(_ context.Context, sp Spawn) (string, error) { return "p", nil }
func (l *noEnvLauncher) LaunchCmd(_ context.Context, sp Spawn, cmd string) (string, error) {
	l.lastCmd = cmd
	return "pane", nil
}

// envRecordingLauncher implements both interfaces and records the env.
type envRecordingLauncher struct {
	lastCmd string
	lastEnv []string
}

func (l *envRecordingLauncher) Launch(_ context.Context, sp Spawn) (string, error) { return "p", nil }
func (l *envRecordingLauncher) LaunchCmd(ctx context.Context, sp Spawn, cmd string) (string, error) {
	return l.LaunchCmdEnv(ctx, sp, cmd, nil)
}
func (l *envRecordingLauncher) LaunchCmdEnv(_ context.Context, sp Spawn, cmd string, env []string) (string, error) {
	l.lastCmd, l.lastEnv = cmd, env
	return "pane", nil
}

func TestLaunchCmdWithEnv_NoEnv_UsesPlainLaunchCmd(t *testing.T) {
	l := &noEnvLauncher{}
	if _, err := launchCmdWithEnv(context.Background(), l, Spawn{Handle: "h"}, "cmd", nil); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if l.lastCmd != "cmd" {
		t.Fatalf("plain LaunchCmd not used: %q", l.lastCmd)
	}
}

func TestLaunchCmdWithEnv_Secrets_RequireEnvLauncher(t *testing.T) {
	// A launcher without envLauncher must be refused when secrets are present —
	// never silently place secrets in the command string.
	if _, err := launchCmdWithEnv(context.Background(), &noEnvLauncher{},
		Spawn{Handle: "h"}, "cmd", []string{"S=x"}); err == nil {
		t.Fatal("expected refusal for non-env launcher with secrets")
	}
	// An env-capable launcher receives the secrets out-of-band.
	l := &envRecordingLauncher{}
	if _, err := launchCmdWithEnv(context.Background(), l,
		Spawn{Handle: "h"}, "cmd", []string{"S=x"}); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !reflect.DeepEqual(l.lastEnv, []string{"S=x"}) {
		t.Fatalf("env not routed: %v", l.lastEnv)
	}
	if strings.Contains(l.lastCmd, "S=x") {
		t.Fatalf("secret leaked into command string: %q", l.lastCmd)
	}
}

func newSecretTestRunner(t *testing.T, team, host string) (*Runner, string) {
	t.Helper()
	seed, pub, err := envseal.GenerateIdentity()
	if err != nil {
		t.Fatalf("identity: %v", err)
	}
	a := &Runner{Client: &Client{Team: team}, HostID: host, hostSeed: seed}
	return a, pub
}

func TestResolveSecretEnv_NoEnvelope(t *testing.T) {
	a, _ := newSecretTestRunner(t, "t1", "h1")
	kv, err := a.resolveSecretEnv(Spawn{Handle: "h"})
	if err != nil || kv != nil {
		t.Fatalf("no envelope should be a no-op: kv=%v err=%v", kv, err)
	}
}

func TestResolveSecretEnv_RoundTrip(t *testing.T) {
	a, pub := newSecretTestRunner(t, "t1", "h1")
	env, err := envseal.Seal(map[string]string{"OPENAI_API_KEY": "sk-x", "DB": "u"},
		pub, "t1", "h1", "p1")
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	kv, err := a.resolveSecretEnv(Spawn{Handle: "h", EnvSecretEnvelope: env})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	want := []string{"DB=u", "OPENAI_API_KEY=sk-x"}
	if !reflect.DeepEqual(kv, want) {
		t.Fatalf("resolved env = %v, want %v", kv, want)
	}
}

func TestResolveSecretEnv_NoIdentity_FailsClosed(t *testing.T) {
	a := &Runner{Client: &Client{Team: "t1"}, HostID: "h1"} // no hostSeed
	_, err := a.resolveSecretEnv(Spawn{Handle: "h", EnvSecretEnvelope: `{"v":1}`})
	if err == nil {
		t.Fatal("expected fail-closed error with no host identity")
	}
}

// TestRealProcSpawner_InjectsEnv proves the secret actually reaches the child
// process environment (M1/M2 persistent-stdio channel, ADR-056 D-5) — and that
// it is NOT in the command string.
func TestRealProcSpawner_InjectsEnv(t *testing.T) {
	stdout, _, kill, err := RealProcSpawner{}.Spawn(
		context.Background(), "printenv E3C2_SECRET", []string{"E3C2_SECRET=injected-ok"})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	defer kill()
	b, _ := io.ReadAll(stdout)
	if !strings.Contains(string(b), "injected-ok") {
		t.Fatalf("secret not visible in child env; child printed %q", string(b))
	}
}

func TestRealProcSpawner_NoEnv_InheritsParent(t *testing.T) {
	// With no extra env, cmd.Env stays unset so the child inherits the parent's
	// environment — the exact pre-E3c behaviour. printenv of an unset var yields
	// empty output (and a non-zero exit we don't inspect).
	stdout, _, kill, err := RealProcSpawner{}.Spawn(
		context.Background(), "printenv E3C2_SECRET || true", nil)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	defer kill()
	b, _ := io.ReadAll(stdout)
	if strings.Contains(string(b), "injected-ok") {
		t.Fatalf("unexpected leaked value: %q", string(b))
	}
}

func TestResolveSecretEnv_WrongHost_FailsClosed(t *testing.T) {
	a, pub := newSecretTestRunner(t, "t1", "h1")
	env, _ := envseal.Seal(map[string]string{"S": "x"}, pub, "t1", "h1", "p1")
	// Same host seed, but the runner now believes it is a different host.
	a.HostID = "h2"
	if _, err := a.resolveSecretEnv(Spawn{Handle: "h", EnvSecretEnvelope: env}); err == nil {
		t.Fatal("expected foreign-host unseal failure")
	}
}
