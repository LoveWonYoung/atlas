//go:build windows

package can_driver

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows/registry"
)

var (
	UsbDeviceDLL syscall.Handle

	UsbScanDevice  uintptr
	UsbOpenDevice  uintptr
	UsbCloseDevice uintptr

	CANInit           uintptr
	CANStartGetMsg    uintptr
	CANGetMsg         uintptr
	CANSendMsg        uintptr
	CANGetCANSpeedArg uintptr

	CANFDInit           uintptr
	CANFDStartGetMsg    uintptr
	CANFDGetMsg         uintptr
	CANFDSendMsg        uintptr
	CANFDGetCANSpeedArg uintptr

	DevHandle [10]int
	DEVIndex  = 0

	toomossMu        sync.Mutex
	toomossSessionMu sync.Mutex
	toomossInUse     bool
	toomossUSBOpened bool
)

func acquireToomossSession() bool {
	toomossSessionMu.Lock()
	defer toomossSessionMu.Unlock()
	if toomossInUse {
		return false
	}
	toomossInUse = true
	return true
}

func releaseToomossSession() {
	toomossSessionMu.Lock()
	toomossInUse = false
	toomossSessionMu.Unlock()
}

func toomossReady() bool {
	return UsbDeviceDLL != 0 && UsbScanDevice != 0 && UsbOpenDevice != 0 && UsbCloseDevice != 0
}

func resetToomossState() {
	UsbDeviceDLL = 0
	UsbScanDevice = 0
	UsbOpenDevice = 0
	UsbCloseDevice = 0
	CANInit = 0
	CANStartGetMsg = 0
	CANGetMsg = 0
	CANSendMsg = 0
	CANGetCANSpeedArg = 0
	CANFDInit = 0
	CANFDStartGetMsg = 0
	CANFDGetMsg = 0
	CANFDSendMsg = 0
	CANFDGetCANSpeedArg = 0
	toomossUSBOpened = false
}

// EnsureToomossLoaded loads USB2XXX.dll and core USB procs (shared by CAN/LIN).
func EnsureToomossLoaded() error {
	return ensureToomossLoaded()
}

func IsToomossUSBOpened() bool {
	toomossSessionMu.Lock()
	defer toomossSessionMu.Unlock()
	return toomossUSBOpened
}

func ensureToomossLoaded() error {
	toomossMu.Lock()
	defer toomossMu.Unlock()

	if toomossReady() {
		return nil
	}

	resetToomossState()

	if err := loadDLLs(); err != nil {
		return err
	}

	if err := loadProcAddresses(); err != nil {
		if UsbDeviceDLL != 0 {
			_ = syscall.FreeLibrary(UsbDeviceDLL)
		}
		resetToomossState()
		return err
	}

	return nil
}

func loadDLLs() error {
	if UsbDeviceDLL != 0 {
		return nil
	}

	if runtime.GOARCH == "386" {
		if registryPath := getRegistryPath(); registryPath != "" {
			fmt.Println("Found registry path:", registryPath)
			libusbPath := filepath.Join(registryPath, "libusb-1.0.dll")
			if _, err := syscall.LoadLibrary(libusbPath); err != nil {
				fmt.Println("Warning: Failed to load libusb-1.0.dll from", libusbPath, "Error:", err)
			}

			usbPath := filepath.Join(registryPath, "USB2XXX.dll")
			if handle, err := syscall.LoadLibrary(usbPath); err == nil {
				UsbDeviceDLL = handle
				fmt.Println("Loaded DLLs from registry path:", registryPath)
				return nil
			} else {
				fmt.Println("Failed to load USB2XXX.dll from", usbPath, "Error:", err)
			}
		} else {
			fmt.Println("Registry path not found")
		}
	}

	libusbPath := filepath.Join(".\\bin", "libusb-1.0.dll")
	if _, err := syscall.LoadLibrary(libusbPath); err != nil {
		log.Printf("Warning: failed to load libusb-1.0.dll from %s: %v", libusbPath, err)
	}

	usbPath := filepath.Join(".\\bin", "USB2XXX.dll")
	handle, err := syscall.LoadLibrary(usbPath)
	if err != nil {
		return fmt.Errorf("failed to load USB2XXX.dll from %s: %w", usbPath, err)
	}
	UsbDeviceDLL = handle
	log.Printf("Loaded DLLs from default path: %s", usbPath)
	return nil
}

func getProc(name string) (uintptr, error) {
	addr, err := syscall.GetProcAddress(UsbDeviceDLL, name)
	if addr == 0 {
		if err == nil {
			err = errors.New("not found")
		}
		return 0, fmt.Errorf("%s: %w", name, err)
	}
	return addr, nil
}

func loadProcAddresses() error {
	if UsbDeviceDLL == 0 {
		return errors.New("USB2XXX.dll not loaded")
	}

	var errs []string
	var err error

	if UsbScanDevice, err = getProc("USB_ScanDevice"); err != nil {
		errs = append(errs, err.Error())
	}
	if UsbOpenDevice, err = getProc("USB_OpenDevice"); err != nil {
		errs = append(errs, err.Error())
	}
	if UsbCloseDevice, err = getProc("USB_CloseDevice"); err != nil {
		errs = append(errs, err.Error())
	}
	loadOptionalProc("CAN_Init", &CANInit)
	loadOptionalProc("CAN_StartGetMsg", &CANStartGetMsg)
	loadOptionalProc("CAN_GetMsg", &CANGetMsg)
	loadOptionalProc("CAN_SendMsg", &CANSendMsg)
	loadOptionalProc("CAN_GetCANSpeedArg", &CANGetCANSpeedArg)

	loadOptionalProc("CANFD_Init", &CANFDInit)
	loadOptionalProc("CANFD_StartGetMsg", &CANFDStartGetMsg)
	loadOptionalProc("CANFD_GetMsg", &CANFDGetMsg)
	loadOptionalProc("CANFD_SendMsg", &CANFDSendMsg)
	loadOptionalProc("CANFD_GetCANSpeedArg", &CANFDGetCANSpeedArg)

	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	if !toomossReady() {
		return errors.New("required Toomoss USB procedures are not available")
	}
	return nil
}

func loadOptionalProc(name string, dest *uintptr) {
	addr, err := getProc(name)
	if err != nil {
		log.Printf("Toomoss proc not available: %s (%v)", name, err)
		*dest = 0
		return
	}
	*dest = addr
}

func getRegistryPath() string {
	const uninstall = `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`

	views := []struct {
		label  string
		access uint32
	}{
		{"64", registry.READ | registry.WOW64_64KEY},
		{"32", registry.READ | registry.WOW64_32KEY},
		{"default", registry.READ},
	}

	for _, view := range views {
		if path := findRegistryPathInView(uninstall, view.label, view.access); path != "" {
			return path
		}
	}

	return ""
}

func dirFromUninstallString(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	s = strings.Trim(s, `"`)
	if i := strings.IndexByte(s, ' '); i > 0 {
		s = s[:i]
	}
	s = strings.Trim(s, `"`)
	if s == "" {
		return ""
	}
	return filepath.Dir(s)
}

func findRegistryPathInView(uninstall, label string, access uint32) string {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, uninstall, access)
	if err != nil {
		fmt.Println("OpenKey HKLM", label, "view failed:", err)
		return ""
	}
	defer func(k registry.Key) {
		err := k.Close()
		if err != nil {

		}
	}(k)

	names, err := k.ReadSubKeyNames(-1)
	if err != nil {
		fmt.Println("ReadSubKeyNames failed:", err)
		return ""
	}

	fmt.Println("HKLM", label, "view entries:", len(names))

	for _, name := range names {
		sk, err := registry.OpenKey(registry.LOCAL_MACHINE, uninstall+`\`+name, access)
		if err != nil {
			continue
		}

		publisher, _, _ := sk.GetStringValue("Publisher")
		displayName, _, _ := sk.GetStringValue("DisplayName")
		install, _, _ := sk.GetStringValue("InstallLocation")
		appPath, _, _ := sk.GetStringValue("Inno Setup: App Path")
		unins, _, _ := sk.GetStringValue("UninstallString")
		err = sk.Close()
		if err != nil {
			return ""
		}

		pubL := strings.ToLower(strings.TrimSpace(publisher))
		dnL := strings.ToLower(strings.TrimSpace(displayName))

		if strings.Contains(pubL, "toomoss") || strings.Contains(dnL, "toomoss") {
			fmt.Println("Matched subkey:", name)
			fmt.Println("  DisplayName:", displayName)
			fmt.Println("  Publisher:", publisher)

			install = strings.TrimSpace(install)
			if install != "" {
				fmt.Println("  InstallLocation:", install)
				return filepath.Clean(install)
			}

			appPath = strings.TrimSpace(appPath)
			if appPath != "" {
				fmt.Println("  AppPath:", appPath)
				return filepath.Clean(appPath)
			}

			if dir := dirFromUninstallString(unins); dir != "" {
				fmt.Println("  From UninstallString:", dir)
				if hasUSB2XXXDLL(dir) {
					return dir
				}
				fmt.Println("  UninstallString path missing USB2XXX.dll")
			}

			fmt.Println("  No usable path fields")
		}
	}

	for _, name := range names {
		sk, err := registry.OpenKey(registry.LOCAL_MACHINE, uninstall+`\`+name, access)
		if err != nil {
			continue
		}

		install, _, _ := sk.GetStringValue("InstallLocation")
		appPath, _, _ := sk.GetStringValue("Inno Setup: App Path")
		unins, _, _ := sk.GetStringValue("UninstallString")
		err = sk.Close()
		if err != nil {
			return ""
		}

		install = strings.TrimSpace(install)
		if install != "" && pathLooksToomoss(install) {
			fmt.Println("Matched InstallLocation by path hint:", name)
			return filepath.Clean(install)
		}

		appPath = strings.TrimSpace(appPath)
		if appPath != "" && pathLooksToomoss(appPath) {
			fmt.Println("Matched AppPath by path hint:", name)
			return filepath.Clean(appPath)
		}

		if dir := dirFromUninstallString(unins); dir != "" && pathLooksToomoss(dir) {
			fmt.Println("Matched UninstallString by path hint:", name)
			return dir
		}
	}

	return ""
}

func hasUSB2XXXDLL(dir string) bool {
	if dir == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(dir, "USB2XXX.dll"))
	return err == nil
}

func pathLooksToomoss(p string) bool {
	pl := strings.ToLower(p)
	return strings.Contains(pl, "toomoss") || strings.Contains(pl, "tcanlinpro")
}

func usbScan() (bool, error) {
	if IsToomossUSBOpened() {
		return true, nil
	}
	if UsbScanDevice == 0 {
		return false, errors.New("USB_ScanDevice not loaded")
	}
	ret, _, callErr := syscall.SyscallN(
		UsbScanDevice,
		uintptr(unsafe.Pointer(&DevHandle[DEVIndex])),
	)
	if callErr != 0 {
		return false, fmt.Errorf("USB_ScanDevice syscall failed: %w", callErr)
	}
	return ret > 0, nil
}

func UsbScan() bool {
	ok, err := usbScan()
	if err != nil {
		log.Printf("USB scan failed: %v", err)
		return false
	}
	return ok
}

func usbOpen() (bool, error) {
	if IsToomossUSBOpened() {
		return true, nil
	}
	if UsbOpenDevice == 0 {
		return false, errors.New("USB_OpenDevice not loaded")
	}
	stateValue, _, callErr := syscall.SyscallN(
		UsbOpenDevice,
		uintptr(DevHandle[DEVIndex]),
	)
	if callErr != 0 {
		return false, fmt.Errorf("USB_OpenDevice syscall failed: %w", callErr)
	}
	ok := stateValue >= 1
	if ok {
		toomossSessionMu.Lock()
		toomossUSBOpened = true
		toomossSessionMu.Unlock()
	}
	return ok, nil
}

func UsbOpen() bool {
	ok, err := usbOpen()
	if err != nil {
		log.Printf("USB open failed: %v", err)
		return false
	}
	return ok
}

func UsbClose() error {
	toomossMu.Lock()
	defer toomossMu.Unlock()

	if UsbDeviceDLL == 0 {
		return nil
	}
	if UsbCloseDevice == 0 {
		return errors.New("USB_CloseDevice not loaded")
	}
	ret, _, callErr := syscall.SyscallN(
		UsbCloseDevice,
		uintptr(DevHandle[DEVIndex]),
	)
	if callErr != 0 {
		return fmt.Errorf("USB_CloseDevice syscall failed: %w", callErr)
	}
	if ret < 1 {
		return fmt.Errorf("USB_CloseDevice returned %d", ret)
	}
	if err := syscall.FreeLibrary(UsbDeviceDLL); err != nil {
		return fmt.Errorf("FreeLibrary failed: %w", err)
	}
	resetToomossState()
	return nil
}
