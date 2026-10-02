//go:build windows

package core

import (
	"context"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

const officialCodexPackageFamily = "OpenAI.Codex_2p2nqsd0c76g0"

type windowsListenerIdentity struct {
	ProcessID uint32 `json:"processID"`
	Address   string `json:"address"`
}

type windowsRegisteredCodex struct {
	ExecutablePath string
	ApplicationID  string
	Version        [4]uint16
}

type windowsProcessRecord struct {
	Identity CodexProcessIdentity
	Parent   *CodexProcessIdentity
}

// These sources expose OS facts, not a second connection policy. Tests supply
// snapshots; the same validator decides process and listener ownership.
type windowsObservationSources struct {
	Package   func(context.Context) (windowsRegisteredCodex, error)
	Processes func(context.Context) ([]windowsProcessRecord, int, error)
	Listeners func(context.Context) ([]windowsListenerIdentity, error)
}

func readNativeWindowsCodexObservation(ctx context.Context) (windowsProcessObservation, error) {
	return readWindowsObservation(ctx, windowsObservationSources{
		Package:   readRegisteredWindowsCodex,
		Processes: readWindowsCodexProcesses,
		Listeners: readWindowsCodexListeners,
	})
}

func readWindowsObservation(ctx context.Context, sources windowsObservationSources) (windowsProcessObservation, error) {
	var result windowsProcessObservation
	if err := ctx.Err(); err != nil {
		return result, err
	}
	pkg, err := sources.Package(ctx)
	if err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	processes, unverified, err := sources.Processes(ctx)
	if err != nil {
		return result, err
	}
	result.ExecutablePath, result.ApplicationID, result.UnverifiedCount = pkg.ExecutablePath, pkg.ApplicationID, unverified
	for _, process := range processes {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if pkg.ExecutablePath == "" || !sameWindowsExecutable(process.Identity.ExecutablePath, pkg.ExecutablePath) {
			result.UnverifiedCount++
			continue
		}
		// A native child shares the registered image and an earlier parent
		// identity. An inaccessible/exited/reused parent is not proof of a main
		// process; keep that observation unavailable rather than guess.
		if process.Parent == nil || process.Parent.StartedAt.After(process.Identity.StartedAt) {
			return result, ErrCodexIdentityUnverified
		}
		if sameWindowsExecutable(process.Parent.ExecutablePath, pkg.ExecutablePath) {
			continue
		}
		identity := process.Identity
		identity.ApplicationID = pkg.ApplicationID
		result.Processes = append(result.Processes, identity)
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	result.Listeners, err = sources.Listeners(ctx)
	return result, err
}

func sameWindowsExecutable(left, right string) bool {
	return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
}

var windowsProcessKernel = syscall.NewLazyDLL("kernel32.dll")
var windowsTCPTable = syscall.NewLazyDLL("iphlpapi.dll").NewProc("GetExtendedTcpTable")

func windowsPackageNames() ([]string, error) {
	api := windowsProcessKernel.NewProc("GetPackagesByPackageFamily")
	if err := api.Find(); err != nil {
		return nil, errors.ErrUnsupported
	}
	family, _ := syscall.UTF16PtrFromString(officialCodexPackageFamily)
	var count, length uint32
	code, _, _ := api.Call(uintptr(unsafe.Pointer(family)), uintptr(unsafe.Pointer(&count)), 0, uintptr(unsafe.Pointer(&length)), 0)
	if code == 0 && count == 0 {
		return nil, nil
	}
	if code != 122 || count == 0 || count > 64 || length == 0 || length > 65536 {
		return nil, fmt.Errorf("%w: package inventory status %d", ErrCodexIdentityUnverified, code)
	}
	names := make([]*uint16, count)
	buffer := make([]uint16, length)
	code, _, _ = api.Call(uintptr(unsafe.Pointer(family)), uintptr(unsafe.Pointer(&count)), uintptr(unsafe.Pointer(&names[0])), uintptr(unsafe.Pointer(&length)), uintptr(unsafe.Pointer(&buffer[0])))
	if code != 0 || count > uint32(len(names)) || length > uint32(len(buffer)) {
		return nil, fmt.Errorf("%w: package inventory changed", ErrCodexIdentityUnverified)
	}
	base, end := uintptr(unsafe.Pointer(&buffer[0])), uintptr(unsafe.Pointer(&buffer[0]))+uintptr(length)*2
	result := make([]string, 0, count)
	for _, name := range names[:count] {
		pointer := uintptr(unsafe.Pointer(name))
		if pointer < base || pointer >= end || (pointer-base)%2 != 0 {
			return nil, ErrCodexIdentityUnverified
		}
		index := (pointer - base) / 2
		value := buffer[index:length]
		null := -1
		for offset, character := range value {
			if character == 0 {
				null = offset
				break
			}
		}
		if null < 0 {
			return nil, ErrCodexIdentityUnverified
		}
		result = append(result, syscall.UTF16ToString(value[:null]))
	}
	return result, nil
}

func windowsStorePackagePath(fullName string) (string, bool, error) {
	originAPI := syscall.NewLazyDLL("kernelbase.dll").NewProc("GetStagedPackageOrigin")
	pathAPI := windowsProcessKernel.NewProc("GetPackagePathByFullName")
	if originAPI.Find() != nil || pathAPI.Find() != nil {
		return "", false, errors.ErrUnsupported
	}
	name, err := syscall.UTF16PtrFromString(fullName)
	if err != nil {
		return "", false, ErrCodexIdentityUnverified
	}
	var origin uint32
	code, _, _ := originAPI.Call(uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(&origin)))
	if code != 0 {
		return "", false, fmt.Errorf("%w: package origin status %d", ErrCodexIdentityUnverified, code)
	}
	// PackageOrigin_Store, defined by the Windows AppModel API. Developer,
	// unsigned and enterprise packages cannot become an attachable identity.
	if origin != 3 {
		return "", false, nil
	}
	var length uint32
	code, _, _ = pathAPI.Call(uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(&length)), 0)
	if code != 122 || length == 0 || length > 32768 {
		return "", false, ErrCodexIdentityUnverified
	}
	buffer := make([]uint16, length)
	code, _, _ = pathAPI.Call(uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(&length)), uintptr(unsafe.Pointer(&buffer[0])))
	if code != 0 || length > uint32(len(buffer)) {
		return "", false, ErrCodexIdentityUnverified
	}
	path := syscall.UTF16ToString(buffer[:length])
	if !filepath.IsAbs(path) {
		return "", false, ErrCodexIdentityUnverified
	}
	return filepath.Clean(path), true, nil
}

func readRegisteredWindowsCodex(ctx context.Context) (windowsRegisteredCodex, error) {
	var empty windowsRegisteredCodex
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	names, err := windowsPackageNames()
	if err != nil {
		return empty, err
	}
	packages := []windowsRegisteredCodex{}
	for _, fullName := range names {
		if err := ctx.Err(); err != nil {
			return empty, err
		}
		directory, trusted, err := windowsStorePackagePath(fullName)
		if err != nil {
			return empty, err
		}
		if !trusted {
			continue
		}
		file, err := os.Open(filepath.Join(directory, "AppxManifest.xml"))
		if err != nil {
			return empty, ErrCodexIdentityUnverified
		}
		data, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
		_ = file.Close()
		if err != nil || len(data) > 1<<20 {
			return empty, ErrCodexIdentityUnverified
		}
		pkg, err := registeredWindowsCodexFromManifest(directory, data)
		if err != nil {
			return empty, err
		}
		packages = append(packages, pkg)
	}
	sort.Slice(packages, func(left, right int) bool {
		for index := range packages[left].Version {
			if packages[left].Version[index] != packages[right].Version[index] {
				return packages[left].Version[index] > packages[right].Version[index]
			}
		}
		return packages[left].ExecutablePath < packages[right].ExecutablePath
	})
	if len(packages) == 0 {
		return empty, nil
	}
	if len(packages) > 1 && packages[0].Version == packages[1].Version {
		return empty, ErrCodexIdentityUnverified
	}
	return packages[0], nil
}

func registeredWindowsCodexFromManifest(directory string, data []byte) (windowsRegisteredCodex, error) {
	var manifest struct {
		Identity struct {
			Name    string `xml:"Name,attr"`
			Version string `xml:"Version,attr"`
		} `xml:"Identity"`
		Applications []struct {
			ID         string `xml:"Id,attr"`
			Executable string `xml:"Executable,attr"`
		} `xml:"Applications>Application"`
	}
	var result windowsRegisteredCodex
	if !filepath.IsAbs(directory) || len(data) > 1<<20 || xml.Unmarshal(data, &manifest) != nil || manifest.Identity.Name != "OpenAI.Codex" {
		return result, ErrCodexIdentityUnverified
	}
	version := strings.Split(manifest.Identity.Version, ".")
	if len(version) != 4 {
		return result, ErrCodexIdentityUnverified
	}
	for index, component := range version {
		value, err := strconv.ParseUint(component, 10, 16)
		if err != nil {
			return result, ErrCodexIdentityUnverified
		}
		result.Version[index] = uint16(value)
	}
	for _, application := range manifest.Applications {
		executable := filepath.Clean(strings.ReplaceAll(application.Executable, "/", string(filepath.Separator)))
		if !strings.EqualFold(filepath.Base(executable), "ChatGPT.exe") {
			continue
		}
		if result.ExecutablePath != "" || filepath.IsAbs(executable) || filepath.VolumeName(executable) != "" ||
			executable == ".." || strings.HasPrefix(executable, ".."+string(filepath.Separator)) || application.ID == "" ||
			strings.ContainsAny(application.ID, " !/\\\t\r\n") {
			return windowsRegisteredCodex{}, ErrCodexIdentityUnverified
		}
		result.ExecutablePath = filepath.Join(directory, executable)
		result.ApplicationID = officialCodexPackageFamily + "!" + application.ID
	}
	if result.ExecutablePath == "" {
		return result, ErrCodexIdentityUnverified
	}
	return result, nil
}

func readWindowsProcessIdentity(pid uint32) (CodexProcessIdentity, error) {
	var result CodexProcessIdentity
	handle, err := syscall.OpenProcess(0x1000, false, pid)
	if err != nil {
		return result, err
	}
	defer syscall.CloseHandle(handle)
	var exit uint32
	if err := syscall.GetExitCodeProcess(handle, &exit); err != nil {
		return result, err
	}
	if exit != 259 {
		return result, ErrCodexTargetExited
	}
	var created, exited, kernel, user syscall.Filetime
	if err := syscall.GetProcessTimes(handle, &created, &exited, &kernel, &user); err != nil {
		return result, err
	}
	name := make([]uint16, 32768)
	length := uint32(len(name))
	api := windowsProcessKernel.NewProc("QueryFullProcessImageNameW")
	if err := api.Find(); err != nil {
		return result, errors.ErrUnsupported
	}
	ok, _, callErr := api.Call(uintptr(handle), 0, uintptr(unsafe.Pointer(&name[0])), uintptr(unsafe.Pointer(&length)))
	if ok == 0 {
		return result, callErr
	}
	if length == 0 || length > uint32(len(name)) {
		return result, ErrCodexIdentityUnverified
	}
	result.ProcessID, result.StartedAt = pid, time.Unix(0, created.Nanoseconds()).UTC()
	result.ExecutablePath = strings.ToLower(filepath.Clean(syscall.UTF16ToString(name[:length])))
	return result, nil
}

func readWindowsCodexProcesses(ctx context.Context) ([]windowsProcessRecord, int, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	handle, err := syscall.CreateToolhelp32Snapshot(2, 0)
	if err != nil {
		return nil, 0, err
	}
	defer syscall.CloseHandle(handle)
	entry := syscall.ProcessEntry32{}
	entry.Size = uint32(unsafe.Sizeof(entry))
	result := []windowsProcessRecord{}
	unverified, count := 0, 0
	err = syscall.Process32First(handle, &entry)
	for err == nil {
		if err := ctx.Err(); err != nil {
			return nil, 0, err
		}
		count++
		if count > 65536 {
			return nil, 0, ErrCodexIdentityUnverified
		}
		if strings.EqualFold(syscall.UTF16ToString(entry.ExeFile[:]), "ChatGPT.exe") {
			identity, identityErr := readWindowsProcessIdentity(entry.ProcessID)
			if identityErr != nil {
				if !errors.Is(identityErr, ErrCodexTargetExited) && !errors.Is(identityErr, syscall.Errno(87)) {
					unverified++
				}
			} else {
				record := windowsProcessRecord{Identity: identity}
				if parent, parentErr := readWindowsProcessIdentity(entry.ParentProcessID); parentErr == nil {
					record.Parent = &parent
				}
				result = append(result, record)
			}
		}
		err = syscall.Process32Next(handle, &entry)
	}
	if !errors.Is(err, syscall.Errno(18)) {
		return nil, 0, err
	}
	return result, unverified, nil
}

func readWindowsCodexListeners(ctx context.Context) ([]windowsListenerIdentity, error) {
	if err := windowsTCPTable.Find(); err != nil {
		return nil, errors.ErrUnsupported
	}
	result := []windowsListenerIdentity{}
	for _, family := range []uintptr{2, 23} {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var size uint32
		code, _, _ := windowsTCPTable.Call(0, uintptr(unsafe.Pointer(&size)), 0, family, 3, 0)
		if code != 122 || size < 4 || size > 8<<20 {
			return nil, ErrCodexIdentityUnverified
		}
		buffer := make([]byte, size)
		for attempt := 0; attempt < 3; attempt++ {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			code, _, _ = windowsTCPTable.Call(uintptr(unsafe.Pointer(&buffer[0])), uintptr(unsafe.Pointer(&size)), 0, family, 3, 0)
			if code != 122 || size <= uint32(len(buffer)) || size > 8<<20 {
				break
			}
			// Other applications can open listeners between the size and data
			// calls. Retry a growing inventory without relaxing ownership checks.
			buffer = make([]byte, size)
		}
		if code != 0 || size > uint32(len(buffer)) {
			return nil, ErrCodexIdentityUnverified
		}
		rows, err := windowsListenersFromTCPTable(buffer[:size], family)
		if err != nil {
			return nil, err
		}
		result = append(result, rows...)
	}
	return result, nil
}

// Native MIB_TCP{,6}ROW_OWNER_PID records contain DWORD network-order ports.
// Parsing both address families prevents an IPv6/wildcard listener from being
// omitted from the existing fail-closed ownership rule.
func windowsListenersFromTCPTable(data []byte, family uintptr) ([]windowsListenerIdentity, error) {
	rowSize, portOffset, pidOffset, stateOffset := 24, 8, 20, 0
	if family == 23 {
		rowSize, portOffset, pidOffset, stateOffset = 56, 20, 52, 48
	} else if family != 2 {
		return nil, ErrCodexIdentityUnverified
	}
	if len(data) < 4 {
		return nil, ErrCodexIdentityUnverified
	}
	count := binary.LittleEndian.Uint32(data[:4])
	if uint64(count)*uint64(rowSize)+4 > uint64(len(data)) {
		return nil, ErrCodexIdentityUnverified
	}
	result := []windowsListenerIdentity{}
	for index := uint32(0); index < count; index++ {
		row := data[4+int(index)*rowSize : 4+int(index+1)*rowSize]
		if binary.BigEndian.Uint16(row[portOffset:portOffset+2]) != 9335 {
			continue
		}
		if binary.LittleEndian.Uint32(row[stateOffset:stateOffset+4]) != 2 {
			return nil, ErrCodexIdentityUnverified
		}
		address := net.IP(row[4:8]).String()
		if family == 23 {
			address = net.IP(row[:16]).String()
			if net.IP(row[:16]).To4() != nil {
				address = "::ffff:" + address
			}
		}
		result = append(result, windowsListenerIdentity{ProcessID: binary.LittleEndian.Uint32(row[pidOffset : pidOffset+4]), Address: address})
	}
	return result, nil
}
