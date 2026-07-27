//go:build windows || (darwin && cgo)

package preset

import "github.com/LoveWonYoung/atlas/can_driver"

func NewPresetToomoss(physId, respId, funcId uint32, channel byte, canType can_driver.CanType) (*Preset, error) {
	drv := can_driver.NewToomoss(canType, channel)
	return newPreset(drv, physId, respId, funcId)
}
