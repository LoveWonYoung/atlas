//go:build windows

package device

import (
	"errors"
	"fmt"

	"github.com/LoveWonYoung/atlas/driver"
	"github.com/LoveWonYoung/atlas/lindriver"
)

func initDevice(config Config) (*Device, error) {
	if config.Bus == BusCAN {
		var dev driver.CANDriver
		switch config.Provider {
		case ProviderAuto:
			dev = driver.NewAutoDriver(config.CAN.Type)
		case ProviderToomoss:
			dev = driver.NewToomoss(config.CAN.Type, config.CAN.Channel)
		case ProviderTSMaster:
			dev = driver.NewTSMaster(config.CAN.Type, config.CAN.Channel)
		case ProviderPCAN:
			dev = driver.NewPCAN(config.CAN.Type, config.CAN.Channel)
		case ProviderVector:
			dev = driver.NewVector(config.CAN.Type, config.CAN.VectorDeviceType, int(config.CAN.Channel))
		default:
			return nil, fmt.Errorf("device: provider %q does not support CAN on Windows", config.Provider)
		}
		return initializedCAN(dev)
	}

	switch config.Provider {
	case ProviderAuto:
		lindriver.Bt = config.LIN.BaudRate
		toomoss, toomossErr := lindriver.NewToomoss(config.LIN.Channels, nativeLINMode(config.LIN.Mode))
		if toomossErr == nil {
			return initializedLIN(toomoss), nil
		}
		if config.LIN.Mode == LINSlave {
			return nil, fmt.Errorf("device: automatic LIN slave initialization failed: %w", toomossErr)
		}
		if config.LIN.BaudRate != 19_200 {
			return nil, fmt.Errorf("device: automatic LIN initialization at %d bit/s failed: %w", config.LIN.BaudRate, toomossErr)
		}
		tsmaster, tsmasterErr := lindriver.NewTSMaster(
			lindriver.TSMasterDeviceType(config.LIN.TSMasterDevice),
			config.LIN.Channels...,
		)
		if tsmasterErr == nil {
			return initializedLIN(tsmaster), nil
		}
		return nil, fmt.Errorf("device: no available LIN device: %w", errors.Join(toomossErr, tsmasterErr))
	case ProviderToomoss:
		lindriver.Bt = config.LIN.BaudRate
		dev, err := lindriver.NewToomoss(config.LIN.Channels, nativeLINMode(config.LIN.Mode))
		if err != nil {
			return nil, err
		}
		return initializedLIN(dev), nil
	case ProviderTSMaster:
		if config.LIN.Mode == LINSlave {
			return nil, errors.New("device: TSMaster LIN driver currently supports master mode only")
		}
		if config.LIN.BaudRate != 19_200 {
			return nil, fmt.Errorf("device: TSMaster LIN driver currently supports 19200 bit/s only, got %d", config.LIN.BaudRate)
		}
		dev, err := lindriver.NewTSMaster(
			lindriver.TSMasterDeviceType(config.LIN.TSMasterDevice),
			config.LIN.Channels...,
		)
		if err != nil {
			return nil, err
		}
		return initializedLIN(dev), nil
	case ProviderMock:
		return initializedLIN(lindriver.NewMockDriver()), nil
	default:
		return nil, fmt.Errorf("device: provider %q does not support LIN on Windows", config.Provider)
	}
}
