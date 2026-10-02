//go:build windows

package core

import (
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func nativeCodexManifest(version, applications string) []byte {
	return []byte("<Package><Identity Name=\"OpenAI.Codex\" Version=\"" + version + "\"/><Applications>" + applications + "</Applications></Package>")
}

func TestWindowsRegisteredManifestRejectsEscapesAndAmbiguity(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "official-package")
	application := "<Application Id=\"App\" Executable=\"app/ChatGPT.exe\"/>"
	pkg, err := registeredWindowsCodexFromManifest(directory, nativeCodexManifest("26.930.2377.0", application))
	if err != nil || pkg.ExecutablePath != filepath.Join(directory, "app", "ChatGPT.exe") || pkg.ApplicationID != officialCodexPackageFamily+"!App" || pkg.Version != [4]uint16{26, 930, 2377, 0} {
		t.Fatalf("registered identity = %#v, %v", pkg, err)
	}
	for _, test := range []struct{ name, version, applications string }{
		{"escaping executable", "1.2.3.4", "<Application Id=\"App\" Executable=\"../ChatGPT.exe\"/>"},
		{"absolute executable", "1.2.3.4", "<Application Id=\"App\" Executable=\"C:/ChatGPT.exe\"/>"},
		{"alternate volume", "1.2.3.4", "<Application Id=\"App\" Executable=\"C:ChatGPT.exe\"/>"},
		{"duplicate executable", "1.2.3.4", application + application},
		{"application id delimiter", "1.2.3.4", "<Application Id=\"App!Other\" Executable=\"ChatGPT.exe\"/>"},
		{"missing executable", "1.2.3.4", "<Application Id=\"App\" Executable=\"Other.exe\"/>"},
		{"overflow version", "1.2.65536.4", application},
		{"missing version component", "1.2.3", application},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := registeredWindowsCodexFromManifest(directory, nativeCodexManifest(test.version, test.applications)); !errors.Is(err, ErrCodexIdentityUnverified) {
				t.Fatalf("untrusted manifest accepted: %v", err)
			}
		})
	}
	for _, data := range [][]byte{
		[]byte(strings.ReplaceAll(string(nativeCodexManifest("1.2.3.4", application)), "OpenAI.Codex", "Other.Codex")),
		[]byte("<Package>"),
		make([]byte, (1<<20)+1),
	} {
		if _, err := registeredWindowsCodexFromManifest(directory, data); !errors.Is(err, ErrCodexIdentityUnverified) {
			t.Fatalf("invalid manifest accepted: %v", err)
		}
	}
}

func TestWindowsNativeObservationSeparatesMainHelpersAndForeignImages(t *testing.T) {
	started := time.Now().UTC().Add(-time.Minute)
	executable := filepath.Join(t.TempDir(), "package", "ChatGPT.exe")
	parent := CodexProcessIdentity{ProcessID: 1, ExecutablePath: filepath.Join(t.TempDir(), "launcher.exe"), StartedAt: started.Add(-time.Minute)}
	main := CodexProcessIdentity{ProcessID: 2, ExecutablePath: executable, StartedAt: started}
	child := CodexProcessIdentity{ProcessID: 3, ExecutablePath: strings.ToUpper(executable), StartedAt: started.Add(time.Second)}
	foreign := CodexProcessIdentity{ProcessID: 4, ExecutablePath: filepath.Join(t.TempDir(), "ChatGPT.exe"), StartedAt: started}
	listeners := []windowsListenerIdentity{{ProcessID: 2, Address: "127.0.0.1"}}
	sources := windowsObservationSources{
		Package: func(context.Context) (windowsRegisteredCodex, error) {
			return windowsRegisteredCodex{ExecutablePath: executable, ApplicationID: officialCodexPackageFamily + "!App"}, nil
		},
		Processes: func(context.Context) ([]windowsProcessRecord, int, error) {
			return []windowsProcessRecord{{Identity: main, Parent: &parent}, {Identity: child, Parent: &main}, {Identity: foreign, Parent: &parent}}, 1, nil
		},
		Listeners: func(context.Context) ([]windowsListenerIdentity, error) { return listeners, nil },
	}
	observation, err := readWindowsObservation(context.Background(), sources)
	if err != nil || len(observation.Processes) != 1 || observation.Processes[0].ProcessID != 2 || observation.Processes[0].ApplicationID != officialCodexPackageFamily+"!App" || observation.UnverifiedCount != 2 || len(observation.Listeners) != 1 {
		t.Fatalf("native observation = %#v, %v", observation, err)
	}
	validated, err := validateWindowsCodexObservation(observation)
	if err != nil || validated.Target == nil || validated.Target.ProcessID != main.ProcessID || !validated.ListenerOwned {
		t.Fatalf("only the registered main process may own attachment: %#v, %v", validated, err)
	}
	for _, candidateParent := range []*CodexProcessIdentity{nil, {ProcessID: 1, ExecutablePath: parent.ExecutablePath, StartedAt: started.Add(time.Second)}} {
		sources.Processes = func(context.Context) ([]windowsProcessRecord, int, error) {
			return []windowsProcessRecord{{Identity: main, Parent: candidateParent}}, 0, nil
		}
		if _, err := readWindowsObservation(context.Background(), sources); !errors.Is(err, ErrCodexIdentityUnverified) {
			t.Fatalf("missing or reused parent accepted: %v", err)
		}
	}
}

func TestWindowsNativeObservationCancellationStopsOSReads(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	sources := windowsObservationSources{
		Package: func(context.Context) (windowsRegisteredCodex, error) {
			calls++
			cancel()
			return windowsRegisteredCodex{}, nil
		},
		Processes: func(context.Context) ([]windowsProcessRecord, int, error) {
			t.Fatal("process read after cancellation")
			return nil, 0, nil
		},
		Listeners: func(context.Context) ([]windowsListenerIdentity, error) {
			t.Fatal("listener read after cancellation")
			return nil, nil
		},
	}
	if _, err := readWindowsObservation(ctx, sources); !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("cancellation = %v, reads = %d", err, calls)
	}
	if _, err := readWindowsObservation(ctx, sources); !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("already canceled request read OS facts: %v, %d", err, calls)
	}
}

func nativeTCPFixture(family uintptr, addresses [][]byte, pids []uint32) []byte {
	rowSize, portOffset, pidOffset, stateOffset := 24, 8, 20, 0
	if family == 23 {
		rowSize, portOffset, pidOffset, stateOffset = 56, 20, 52, 48
	}
	data := make([]byte, 4+rowSize*len(addresses))
	binary.LittleEndian.PutUint32(data[:4], uint32(len(addresses)))
	for index, address := range addresses {
		row := data[4+index*rowSize : 4+(index+1)*rowSize]
		addressOffset := 4
		if family == 23 {
			addressOffset = 0
		}
		copy(row[addressOffset:], address)
		binary.LittleEndian.PutUint32(row[stateOffset:stateOffset+4], 2)
		binary.BigEndian.PutUint16(row[portOffset:portOffset+2], 9335)
		binary.LittleEndian.PutUint32(row[pidOffset:pidOffset+4], pids[index])
	}
	return data
}

func TestWindowsNativeTCPTableRetainsEveryConflictingListener(t *testing.T) {
	data := nativeTCPFixture(2, [][]byte{{127, 0, 0, 1}, {0, 0, 0, 0}}, []uint32{2, 99})
	listeners, err := windowsListenersFromTCPTable(data, 2)
	if err != nil || len(listeners) != 2 || listeners[0] != (windowsListenerIdentity{ProcessID: 2, Address: "127.0.0.1"}) || listeners[1].Address != "0.0.0.0" || listeners[1].ProcessID != 99 {
		t.Fatalf("IPv4 ownership = %#v, %v", listeners, err)
	}
	loopback, mapped := make([]byte, 16), make([]byte, 16)
	loopback[15] = 1
	mapped[10], mapped[11], mapped[12], mapped[15] = 255, 255, 127, 1
	v6 := nativeTCPFixture(23, [][]byte{loopback, mapped, make([]byte, 16)}, []uint32{2, 2, 99})
	listeners, err = windowsListenersFromTCPTable(v6, 23)
	if err != nil || len(listeners) != 3 || listeners[0].Address != "::1" || listeners[1].Address != "::ffff:127.0.0.1" || listeners[2].Address != "::" {
		t.Fatalf("IPv6 listeners lost or mapped to trusted IPv4 = %#v, %v", listeners, err)
	}
	for _, invalid := range [][]byte{{}, data[:len(data)-1], {255, 255, 255, 255}} {
		if _, err := windowsListenersFromTCPTable(invalid, 2); !errors.Is(err, ErrCodexIdentityUnverified) {
			t.Fatalf("malformed TCP table accepted: %v", err)
		}
	}
	binary.LittleEndian.PutUint32(data[4:8], 5)
	if _, err := windowsListenersFromTCPTable(data, 2); !errors.Is(err, ErrCodexIdentityUnverified) {
		t.Fatalf("non-listening state accepted: %v", err)
	}
}

func TestWindowsNativeReadOwnProcessIdentityAndListenerInventory(t *testing.T) {
	identity, err := readWindowsProcessIdentity(uint32(os.Getpid()))
	executable, executableErr := os.Executable()
	if err != nil || executableErr != nil || !sameWindowsExecutable(identity.ExecutablePath, executable) || identity.ProcessID != uint32(os.Getpid()) || identity.StartedAt.IsZero() || identity.StartedAt.After(time.Now()) {
		t.Fatalf("own process metadata = %#v, %v, %v", identity, err, executableErr)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := readWindowsCodexListeners(ctx); err != nil {
		t.Fatalf("native read-only listener inventory: %v", err)
	}
}
