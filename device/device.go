// Package device provides one initialization entry point for CAN and LIN
// adapters supported by atlas.
package device

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/LoveWonYoung/atlas/driver"
	"github.com/LoveWonYoung/atlas/liniface"
)

// Bus identifies the bus exposed by a device instance.
type Bus string

const (
	BusCAN Bus = "can"
	BusLIN Bus = "lin"
)

// Provider identifies the hardware vendor or automatic selection strategy.
type Provider string

const (
	ProviderAuto     Provider = "auto"
	ProviderToomoss  Provider = "toomoss"
	ProviderTSMaster Provider = "tsmaster"
	ProviderPCAN     Provider = "pcan"
	ProviderVector   Provider = "vector"
	ProviderMock     Provider = "mock"
)

// LINMode selects the role used to initialize a LIN channel.
type LINMode byte

const (
	// LINMaster is the zero value so an omitted mode uses the common diagnostic role.
	LINMaster LINMode = iota
	LINSlave
)

// TSMasterDevice identifies the TOSUN adapter model used for channel mapping.
type TSMasterDevice byte

const (
	TSMasterTC1016 TSMasterDevice = 11
	TSMasterTL1001 TSMasterDevice = 4
)

// CANConfig contains CAN/CAN-FD-specific settings.
type CANConfig struct {
	Type             driver.CanType
	Channel          byte
	VectorDeviceType int
}

// LINConfig contains LIN-specific settings.
type LINConfig struct {
	Channels       []liniface.Channel
	BaudRate       uint
	Mode           LINMode
	TSMasterDevice TSMasterDevice
}

// Config selects a bus and a provider. Zero-value CAN and LIN fields use
// conservative defaults (classic CAN, channel 0, LIN master at 19.2 kbit/s).
type Config struct {
	Bus      Bus
	Provider Provider
	CAN      CANConfig
	LIN      LINConfig
}

// Device owns one initialized CAN or LIN adapter.
type Device struct {
	canDriver driver.CANDriver
	linDriver liniface.Driver
	close     func() error
	closeOnce sync.Once
	closeErr  error
}

// Init is the common initialization entry point for all supported bus devices.
// CAN devices are initialized and started before Init returns.
func Init(config Config) (*Device, error) {
	config = normalize(config)
	if err := validate(config); err != nil {
		return nil, err
	}
	dev, err := initDevice(config)
	if err != nil {
		return nil, fmt.Errorf("device: initialize %s with %s: %w", config.Bus, config.Provider, err)
	}
	return dev, nil
}

// Open is an alias for Init for callers that prefer resource-oriented naming.
func Open(config Config) (*Device, error) { return Init(config) }

// CANDriver returns the initialized CAN driver, or nil for a LIN device.
func (d *Device) CANDriver() driver.CANDriver {
	if d == nil {
		return nil
	}
	return d.canDriver
}

// LINDriver returns the initialized LIN driver, or nil for a CAN device.
func (d *Device) LINDriver() liniface.Driver {
	if d == nil {
		return nil
	}
	return d.linDriver
}

// Close releases the adapter. It is safe to call more than once.
func (d *Device) Close() error {
	if d == nil {
		return nil
	}
	d.closeOnce.Do(func() {
		if d.close != nil {
			d.closeErr = d.close()
		}
	})
	return d.closeErr
}

func normalize(config Config) Config {
	config.Bus = Bus(strings.ToLower(strings.TrimSpace(string(config.Bus))))
	config.Provider = Provider(strings.ToLower(strings.TrimSpace(string(config.Provider))))
	if config.Bus == "" {
		config.Bus = BusCAN
	}
	if config.Provider == "" {
		config.Provider = ProviderAuto
	}
	if len(config.LIN.Channels) == 0 {
		config.LIN.Channels = []liniface.Channel{0}
	} else {
		config.LIN.Channels = append([]liniface.Channel(nil), config.LIN.Channels...)
	}
	if config.LIN.BaudRate == 0 {
		config.LIN.BaudRate = 19_200
	}
	if config.LIN.TSMasterDevice == 0 {
		config.LIN.TSMasterDevice = TSMasterTC1016
	}
	if config.CAN.VectorDeviceType == 0 {
		config.CAN.VectorDeviceType = driver.CANOEVN1640
	}
	return config
}

func validate(config Config) error {
	switch config.Bus {
	case BusCAN, BusLIN:
	default:
		return fmt.Errorf("device: unsupported bus %q", config.Bus)
	}

	switch config.Provider {
	case ProviderAuto, ProviderToomoss, ProviderTSMaster, ProviderPCAN, ProviderVector, ProviderMock:
	default:
		return fmt.Errorf("device: unsupported provider %q", config.Provider)
	}

	if config.CAN.Type != driver.CAN && config.CAN.Type != driver.CANFD {
		return fmt.Errorf("device: unsupported CAN type %d", config.CAN.Type)
	}
	if config.LIN.Mode != LINMaster && config.LIN.Mode != LINSlave {
		return fmt.Errorf("device: unsupported LIN mode %d", config.LIN.Mode)
	}
	seen := make(map[liniface.Channel]struct{}, len(config.LIN.Channels))
	for _, channel := range config.LIN.Channels {
		if _, ok := seen[channel]; ok {
			return fmt.Errorf("device: duplicate LIN channel %d", channel)
		}
		seen[channel] = struct{}{}
	}
	if config.Bus == BusCAN && config.Provider == ProviderMock {
		return errors.New("device: mock provider currently supports LIN only")
	}
	if config.Bus == BusLIN {
		switch config.Provider {
		case ProviderPCAN, ProviderVector:
			return fmt.Errorf("device: provider %q does not support LIN", config.Provider)
		}
	}
	return nil
}

func initializedCAN(dev driver.CANDriver) (*Device, error) {
	if err := dev.Init(); err != nil {
		return nil, err
	}
	dev.Start()
	return &Device{
		canDriver: dev,
		close: func() error {
			dev.Stop()
			return nil
		},
	}, nil
}

func nativeLINMode(mode LINMode) byte {
	if mode == LINSlave {
		return 0
	}
	return 1
}

type errorCloser interface{ Close() error }
type closer interface{ Close() }

func initializedLIN(dev liniface.Driver) *Device {
	return &Device{
		linDriver: dev,
		close: func() error {
			switch value := dev.(type) {
			case errorCloser:
				return value.Close()
			case closer:
				value.Close()
			}
			return nil
		},
	}
}
