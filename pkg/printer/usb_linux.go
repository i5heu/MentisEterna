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
	"log"
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
		log.Printf("printer: cannot scan /sys/bus/usb/devices: %v", err)
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

	log.Printf("printer: found %d USB device(s) matching %04x:%04x", len(found), vendorID, productID)
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
	log.Printf("printer: opening USB device %04x:%04x at %s (epOut=0x%02x)", dev.vendor, dev.product, devPath, dev.epOut)
	f, err := os.OpenFile(devPath, os.O_RDWR, 0)
	if err != nil {
		log.Printf("printer: open %s failed: %v", devPath, err)
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
		log.Printf("printer: claim interface 0 on %s failed: %v (is another driver like usblp holding it?)", devPath, errno)
		return nil, fmt.Errorf("printer: claim interface 0 on %s: %v (is another driver like usblp holding it?)", devPath, errno)
	}

	log.Printf("printer: successfully claimed interface 0 on %s", devPath)

	return &usbDevFSPrinter{f: f, epOut: dev.epOut}, nil
}

// Write sends a bulk transfer to the OUT endpoint.
func (p *usbDevFSPrinter) Write(data []byte) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}

	log.Printf("printer: sending %d bytes to ep 0x%02x", len(data), p.epOut)
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
		log.Printf("printer: bulk write to ep 0x%02x failed after sending %d bytes: %v", p.epOut, len(data), errno)
		return 0, fmt.Errorf("printer: bulk write to ep 0x%02x: %v", p.epOut, errno)
	}

	log.Printf("printer: bulk write to ep 0x%02x succeeded (%d bytes)", p.epOut, len(data))
	return len(data), nil
}

// Close releases the interface and closes the device.
func (p *usbDevFSPrinter) Close() error {
	// Release interface 0.
	iface := uint32(0)
	if _, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		p.f.Fd(),
		usbdevfsReleaseInterface,
		uintptr(unsafe.Pointer(&iface)),
	); errno != 0 {
		log.Printf("printer: release interface 0 failed: %v", errno)
	}
	if err := p.f.Close(); err != nil {
		log.Printf("printer: close device failed: %v", err)
		return err
	}
	return nil
}

// FindUSBByID locates a USB printer by vendor and product ID using raw
// usbdevfs (matching Python's escpos.printer.Usb(vendor, product)).
// This is the preferred method on Linux systems without the usblp
// kernel module loaded.
//
// Example: FindUSBByID(0x08A6, 0x003D) — Epson TM-T88III.
func FindUSBByID(vendorID, productID uint16) (Printer, error) {
	log.Printf("printer: searching for USB device %04x:%04x", vendorID, productID)
	devs, err := findUSBDevices(vendorID, productID)
	if err != nil {
		return nil, err
	}
	if len(devs) == 0 {
		log.Printf("printer: USB device %04x:%04x not found in /sys/bus/usb/devices", vendorID, productID)
		return nil, fmt.Errorf("printer: USB device %04x:%04x not found", vendorID, productID)
	}

	return newUSBDevFSPrinter(devs[0])
}

// FindPrinter tries multiple strategies to locate a thermal receipt printer:
//  1. THERMAL_PRINTER_DEVICE env var (explicit device path, e.g. /dev/usb/lp0)
//  2. /dev/usb/lp* (usblp kernel module)
//  3. Raw USB by vendor/product ID from THERMAL_PRINTER_USB_ID env var
//     (format: "vid:pid", e.g. "08a6:003d")
//
// Returns the first successful connection.
func FindPrinter() (Printer, error) {
	// Strategy 1: explicit device path from env var.
	if dev := os.Getenv("THERMAL_PRINTER_DEVICE"); dev != "" {
		log.Printf("printer: trying THERMAL_PRINTER_DEVICE=%s", dev)
		if pr, err := NewFilePrinter(dev); err == nil {
			log.Printf("printer: connected via THERMAL_PRINTER_DEVICE=%s", dev)
			return pr, nil
		} else {
			log.Printf("printer: THERMAL_PRINTER_DEVICE=%s failed: %v", dev, err)
		}
	}

	// Strategy 2: usblp character device auto-detect.
	log.Printf("printer: trying /dev/usb/lp* auto-detect")
	if lp, err := FindUSBLP(); err == nil {
		log.Printf("printer: connected via usblp device node")
		return lp, nil
	} else {
		log.Printf("printer: /dev/usb/lp* auto-detect failed: %v", err)
	}

	// Strategy 3: raw USB by THERMAL_PRINTER_USB_ID (format: "vid:pid").
	if vid, pid, ok := PrinterUSBID(); ok {
		log.Printf("printer: trying raw USB %04x:%04x from THERMAL_PRINTER_USB_ID", vid, pid)
		if pr, err := FindUSBByID(vid, pid); err == nil {
			return pr, nil
		} else {
			log.Printf("printer: raw USB %04x:%04x failed: %v", vid, pid, err)
		}
	}

	log.Printf("printer: all discovery strategies exhausted — no printer found")
	return nil, fmt.Errorf(
		"printer: no thermal printer found (tried THERMAL_PRINTER_DEVICE, /dev/usb/lp*)",
	)
}

// parseUSBID parses a "vid:pid" string (e.g. "08a6:003d") into two uint16 values.
func parseUSBID(s string) (vid, pid uint16, ok bool) {
	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 {
		return 0, 0, false
	}
	v, err := strconv.ParseUint(strings.TrimSpace(parts[0]), 16, 16)
	if err != nil {
		return 0, 0, false
	}
	p, err := strconv.ParseUint(strings.TrimSpace(parts[1]), 16, 16)
	if err != nil {
		return 0, 0, false
	}
	return uint16(v), uint16(p), true
}

// PrinterUSBID returns the USB vendor and product IDs from the
// THERMAL_PRINTER_USB_ID environment variable (format: "vid:pid", e.g. "08a6:003d").
// Returns (0, 0, false) if the variable is not set or invalid.
func PrinterUSBID() (vid, pid uint16, ok bool) {
	s := strings.TrimSpace(os.Getenv("THERMAL_PRINTER_USB_ID"))
	if s == "" {
		return 0, 0, false
	}
	return parseUSBID(s)
}
