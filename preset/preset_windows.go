//go:build windows

package preset

import "github.com/LoveWonYoung/atlas/can_driver"

func NewPresetTSMaster(physId, respId, funcId uint32, channel byte, canType can_driver.CanType, deviceType int) (*Preset, error) {
	drv := can_driver.NewTSMaster(canType, channel, deviceType)
	return newPreset(drv, physId, respId, funcId)
}

func NewPresetPCAN(physId, respId, funcId uint32, channel byte, canType can_driver.CanType) (*Preset, error) {
	drv := can_driver.NewPCAN(canType, channel)
	return newPreset(drv, physId, respId, funcId)
}

func NewPresetVector(physId, respId, funcId uint32, channel byte, canType can_driver.CanType, deviceType int) (*Preset, error) {
	drv := can_driver.NewVector(canType, deviceType, int(channel))
	return newPreset(drv, physId, respId, funcId)
}
