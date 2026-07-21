package device

import (
	"testing"
	"time"

	"github.com/LoveWonYoung/atlas/liniface"
)

func TestInitLINMock(t *testing.T) {
	dev, err := Init(Config{Bus: BusLIN, Provider: ProviderMock})
	if err != nil {
		t.Fatal(err)
	}
	if dev.CANDriver() != nil {
		t.Fatal("CAN driver should be nil for a LIN device")
	}
	lin := dev.LINDriver()
	if lin == nil {
		t.Fatal("LIN driver is nil")
	}

	event := &liniface.LinEvent{EventID: 0x22, EventPayload: []byte{1, 2}}
	if err := lin.WriteMessage(event, 0); err != nil {
		t.Fatal(err)
	}
	received, err := lin.ReadEvent(time.Second, 0)
	if err != nil {
		t.Fatal(err)
	}
	if received == nil || received.EventID != event.EventID {
		t.Fatalf("received event = %#v", received)
	}

	if err := dev.Close(); err != nil {
		t.Fatal(err)
	}
	if err := dev.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestInitRejectsDuplicateLINChannels(t *testing.T) {
	_, err := Init(Config{
		Bus:      BusLIN,
		Provider: ProviderMock,
		LIN: LINConfig{
			Channels: []liniface.Channel{1, 1},
		},
	})
	if err == nil {
		t.Fatal("expected duplicate channel error")
	}
}
