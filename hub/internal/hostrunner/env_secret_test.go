package hostrunner

import (
	"context"
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

func TestLaunchM1_SecretsFailClosed(t *testing.T) {
	// M1 persistent-stdio secret injection lands in E3c-2; until then a
	// secret-bearing M1 spawn is refused before any setup (fail-closed) so the
	// runner's fallback ladder can try M2/M4.
	_, err := launchM1(context.Background(), M1LaunchConfig{
		Spawn:     Spawn{Handle: "h", Kind: "codex"},
		SecretEnv: []string{"S=x"},
	})
	if err == nil || !strings.Contains(err.Error(), "E3c-2") {
		t.Fatalf("expected E3c-2 fail-closed error, got %v", err)
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
