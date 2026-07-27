//go:build windows

package preset

import "github.com/LoveWonYoung/atlas/can_driver"

func NewPresetAuto(physId, respId, funcId uint32, canType can_driver.CanType) (*Preset, error) {
	return newPreset(can_driver.NewAutoDriver(canType), physId, respId, funcId)
}
