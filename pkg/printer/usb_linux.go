// Package printer — raw USB (usbdevfs / libusb-style) support.
//
// This implements the same USB communication path as Python's
// escpos.printer.Usb(vendor, product), using Linux usbdevfs ioctls
// on /dev/bus/usb/BBB/DDD device nodes.  No CGo or external dependencies.
//
// This file is Linux-only.

package printer

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

// USB device I/O control codes (from <linux/usbdevice_fs.h>).
// These match the Python pyusb/libusb path.

const (
	usbdevfsClaimInterface   = 0x8004550f // USBDEVFS_CLAIMINTERFACE
	usbdevfsReleaseInterface = 0x80045510 // USBDEVFS_RELEASEINTERFACE
	usbdevfsBulk             = 0xc0185502 // USBDEVFS_BULK
)

// usbdevfsBulkTransfer is the struct for USBDEVFS_BULK ioctl.
type usbdevfsBulkTransfer struct {
	ep      uint32
	len     uint32
	timeout uint32
	data    uintptr
}

// usbDevice represents a USB device found by vendor/product ID.
type usbDevice struct {
	vendor  uint16
	product uint16
	bus     int
	dev     int
	epOut   uint8 // bulk OUT endpoint address
}

// findUSBDevices scans /sys/bus/usb/devices/ for devices matching the given
// vendor and product IDs. Returns a slice of matching device descriptors.
func findUSBDevices(vendorID, productID uint16) ([]usbDevice, error) {
	entries, err := os.ReadDir("/sys/bus/usb/devices/")
	if err != nil {
		return nil, fmt.Errorf("printer: scan /sys/bus/usb/devices: %w", err)
	}

	var found []usbDevice
	for _, e := range entries {
		name := e.Name()
		// Skip interfaces (e.g., "1-1:1.0").
		if strings.Contains(name, ":") {
			continue
		}

		devPath := filepath.Join("/sys/bus/usb/devices", name)

		// Read idVendor.
		idVendorStr, err := os.ReadFile(filepath.Join(devPath, "idVendor"))
		if err != nil {
			continue
		}
		idVendor, err := strconv.ParseUint(strings.TrimSpace(string(idVendorStr)), 16, 16)
		if err != nil || uint16(idVendor) != vendorID {
			continue
		}

		// Read idProduct.
		idProductStr, err := os.ReadFile(filepath.Join(devPath, "idProduct"))
		if err != nil {
			continue
		}
		idProduct, err := strconv.ParseUint(strings.TrimSpace(string(idProductStr)), 16, 16)
		if err != nil || uint16(idProduct) != productID {
			continue
		}

		// Read bus number.
		busStr, err := os.ReadFile(filepath.Join(devPath, "busnum"))
		if err != nil {
			continue
		}
		bus, err := strconv.Atoi(strings.TrimSpace(string(busStr)))
		if err != nil {
			continue
		}

		// Read device number.
		devStr, err := os.ReadFile(filepath.Join(devPath, "devnum"))
		if err != nil {
			continue
		}
		dev, err := strconv.Atoi(strings.TrimSpace(string(devStr)))
		if err != nil {
			continue
		}

		// Try to find a bulk OUT endpoint by scanning interface descriptors.
		epOut := findBulkOutEndpoint(devPath)
		if epOut == 0 {
			// Fallback: try common endpoint addresses for TM-T88 printers.
			// The Python demo's default out_ep is 0x01.
			epOut = 0x01
		}

		found = append(found, usbDevice{
			vendor:  vendorID,
			product: productID,
			bus:     bus,
			dev:     dev,
			epOut:   epOut,
		})
	}

	return found, nil
}

// findBulkOutEndpoint scans the sysfs descriptors for a bulk OUT endpoint.
// Returns the endpoint address, or 0 if not found.
func findBulkOutEndpoint(devPath string) uint8 {
	// Look for interface descriptors under the device path.
	// E.g., /sys/bus/usb/devices/1-1/1-1:1.0/
	entries, err := os.ReadDir(devPath)
	if err != nil {
		return 0
	}

	for _, entry := range entries {
		if !strings.Contains(entry.Name(), ":") {
			continue
		}
		ifacePath := filepath.Join(devPath, entry.Name())
		eps, err := os.ReadDir(ifacePath)
		if err != nil {
			continue
		}
		for _, ep := range eps {
			epName := ep.Name()
			if !strings.HasPrefix(epName, "ep_") {
				continue
			}
			// Read direction and type.
			direction, _ := os.ReadFile(filepath.Join(ifacePath, epName, "direction"))
			epType, _ := os.ReadFile(filepath.Join(ifacePath, epName, "type"))
			dirStr := strings.TrimSpace(string(direction))
			typeStr := strings.TrimSpace(string(epType))

			if dirStr == "out" && typeStr == "Bulk" {
				// Read the bEndpointAddress directly — it already contains
				// the correct address (e.g. 0x01 for ep_01 OUT, 0x82 for ep_82 IN).
				addrBytes, err := os.ReadFile(filepath.Join(ifacePath, epName, "bEndpointAddress"))
				if err != nil {
					continue
				}
				addr, err := strconv.ParseUint(strings.TrimSpace(string(addrBytes)), 16, 8)
				if err == nil {
					return uint8(addr)
				}
			}
		}
	}

	return 0
}

// usbDevFSPrinter holds an open /dev/bus/usb/... file descriptor.
// It claims the interface on open and releases it on close.
type usbDevFSPrinter struct {
	f     *os.File
	epOut uint8
}

// newUSBDevFSPrinter opens a USB device via /dev/bus/usb/<bus>/<dev>,
// claims interface 0, and returns a Printer ready for bulk writes.
func newUSBDevFSPrinter(dev usbDevice) (*usbDevFSPrinter, error) {
	devPath := fmt.Sprintf("/dev/bus/usb/%03d/%03d", dev.bus, dev.dev)
	f, err := os.OpenFile(devPath, os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("printer: open %s: %w", devPath, err)
	}

	// Claim interface 0 (like pyusb does after detaching kernel driver).
	iface := uint32(0)
	if _, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		f.Fd(),
		usbdevfsClaimInterface,
		uintptr(unsafe.Pointer(&iface)),
	); errno != 0 {
		f.Close()
		// Try to detach kernel driver first, then retry.
		return nil, fmt.Errorf("printer: claim interface 0 on %s: %v (is another driver like usblp holding it?)", devPath, errno)
	}

	return &usbDevFSPrinter{f: f, epOut: dev.epOut}, nil
}

// Write sends a bulk transfer to the OUT endpoint.
func (p *usbDevFSPrinter) Write(data []byte) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}

	bulk := usbdevfsBulkTransfer{
		ep:      uint32(p.epOut),
		len:     uint32(len(data)),
		timeout: 5000, // 5 second timeout (matching Python's default 0 = no timeout)
		data:    uintptr(unsafe.Pointer(&data[0])),
	}

	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		p.f.Fd(),
		usbdevfsBulk,
		uintptr(unsafe.Pointer(&bulk)),
	)
	if errno != 0 {
		return 0, fmt.Errorf("printer: bulk write to ep 0x%02x: %v", p.epOut, errno)
	}

	return len(data), nil
}

// Close releases the interface and closes the device.
func (p *usbDevFSPrinter) Close() error {
	// Release interface 0.
	iface := uint32(0)
	syscall.Syscall(
		syscall.SYS_IOCTL,
		p.f.Fd(),
		usbdevfsReleaseInterface,
		uintptr(unsafe.Pointer(&iface)),
	)
	return p.f.Close()
}

// FindUSBByID locates a USB printer by vendor and product ID using raw
// usbdevfs (matching Python's escpos.printer.Usb(vendor, product)).
// This is the preferred method on Linux systems without the usblp
// kernel module loaded.
//
// Example: FindUSBByID(0x08A6, 0x003D) — Epson TM-T88III.
func FindUSBByID(vendorID, productID uint16) (Printer, error) {
	devs, err := findUSBDevices(vendorID, productID)
	if err != nil {
		return nil, err
	}
	if len(devs) == 0 {
		return nil, fmt.Errorf("printer: USB device %04x:%04x not found", vendorID, productID)
	}

	return newUSBDevFSPrinter(devs[0])
}

// FindPrinter tries multiple strategies to locate a thermal receipt printer:
//  1. THERMAL_PRINTER_DEVICE env var (explicit device path, e.g. /dev/usb/lp0)
//  2. /dev/usb/lp* (usblp kernel module)
//  3. Raw USB by vendor/product ID from THERMAL_PRINTER_VID / THERMAL_PRINTER_PID
//     env vars (default: 08a6:003d — Epson TM-T88III)
//  4. Raw USB by vendor/product ID 0x04b8:0x0202 (Epson TM-T88IV)
//
// Returns the first successful connection.
func FindPrinter() (Printer, error) {
	// Strategy 1: explicit device path from env var.
	if dev := os.Getenv("THERMAL_PRINTER_DEVICE"); dev != "" {
		if pr, err := NewFilePrinter(dev); err == nil {
			return pr, nil
		}
	}

	// Strategy 2: usblp character device auto-detect.
	if lp, err := FindUSBLP(); err == nil {
		return lp, nil
	}

	// Strategy 3: raw USB by configured vendor/product ID.
	vid, pid := PrinterUSBIDs()
	if pr, err := FindUSBByID(vid, pid); err == nil {
		return pr, nil
	}

	// Strategy 4: Epson TM-T88IV (common variant) — only if the user hasn't
	// configured a custom VID/PID.
	defaultVID, defaultPID := uint16(0x08A6), uint16(0x003D)
	if vid == defaultVID && pid == defaultPID {
		if pr, err := FindUSBByID(0x04b8, 0x0202); err == nil {
			return pr, nil
		}
	}

	return nil, fmt.Errorf(
		"printer: no thermal printer found (tried THERMAL_PRINTER_DEVICE, /dev/usb/lp*, USB %04x:%04x)",
		vid, pid,
	)
}

// PrinterUSBIDs returns the USB vendor and product IDs to use for printer
// discovery.  Reads from THERMAL_PRINTER_VID and THERMAL_PRINTER_PID
// environment variables (hex format, e.g. "08a6" and "003d").
// Defaults to 0x08A6:0x003D (Epson TM-T88III).
func PrinterUSBIDs() (vid, pid uint16) {
	vid = 0x08A6
	pid = 0x003D

	if s := os.Getenv("THERMAL_PRINTER_VID"); s != "" {
		if v, err := strconv.ParseUint(s, 16, 16); err == nil {
			vid = uint16(v)
		}
	}
	if s := os.Getenv("THERMAL_PRINTER_PID"); s != "" {
		if v, err := strconv.ParseUint(s, 16, 16); err == nil {
			pid = uint16(v)
		}
	}
	return vid, pid
}
