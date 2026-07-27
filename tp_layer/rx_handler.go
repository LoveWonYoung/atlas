package tp_layer

import (
	"context"
	"errors"
	"fmt"
)

// ProcessRx Modified to take txChan to allow sending FlowControl frames directly
func (t *Transport) ProcessRx(msg CanMessage, txChan chan<- CanMessage) {
	t.processRx(context.Background(), msg, txChan)
}

func (t *Transport) processRx(ctx context.Context, msg CanMessage, txChan chan<- CanMessage) {
	if !t.address.IsForMe(&msg) {
		return
	}
	frame, err := ParseFrame(&msg)
	if err != nil {
		t.fireError(fmt.Errorf("报文解析失败: %v", err))
		return
	}

	switch f := frame.(type) {
	case *FlowControlFrame:
		if t.rxState == StateWaitCF {
			if f.FlowStatus == FlowStatusWait || f.FlowStatus == FlowStatusContinueToSend {
				t.resetRxTimer()
			}
		}
		t.handleTxFlowControl(f)

	case *SingleFrame:
		t.handleRxSingleFrame(f)

	case *FirstFrame:
		t.handleRxFirstFrame(ctx, f, txChan)

	case *ConsecutiveFrame:
		t.handleRxConsecutiveFrame(ctx, f, txChan)
	}
}

func (t *Transport) handleRxSingleFrame(f *SingleFrame) {
	if t.rxState != StateIdle {
		t.fireError(errors.New("警告：在多帧接收过程中被一个新单帧打断"))
	}
	t.stopReceiving()
	select {
	case t.rxDataChan <- f.Data:
	default:
		t.fireError(ErrReceiveQueueFull)
	}
}

func (t *Transport) handleRxFirstFrame(ctx context.Context, f *FirstFrame, txChan chan<- CanMessage) {
	if t.rxState != StateIdle {
		t.fireError(errors.New("警告：在多帧接收过程中被一个新首帧打断"))
	}
	t.stopReceiving()

	t.rxFrameLen = f.TotalSize
	t.rxBuffer = make([]byte, 0, f.TotalSize) // Optimize allocation
	t.rxBuffer = append(t.rxBuffer, f.Data...)

	if len(t.rxBuffer) >= t.rxFrameLen {
		select {
		case t.rxDataChan <- t.rxBuffer:
		default:
			t.fireError(ErrReceiveQueueFull)
		}
		t.stopReceiving()
	} else {
		t.rxState = StateWaitCF
		t.rxSeqNum = 1
		t.sendFlowControl(ctx, FlowStatusContinueToSend, txChan)
		t.resetRxTimer()
	}
}

func (t *Transport) handleRxConsecutiveFrame(ctx context.Context, f *ConsecutiveFrame, txChan chan<- CanMessage) {
	if t.rxState != StateWaitCF {
		// Ignore unexpected CF
		return
	}

	if f.SequenceNumber != t.rxSeqNum {
		t.fireError(fmt.Errorf("错误：序列号不匹配。期望: %d,收到: %d", t.rxSeqNum, f.SequenceNumber))
		t.stopReceiving()
		return
	}

	t.resetRxTimer()
	t.rxSeqNum = (t.rxSeqNum + 1) % 16

	bytesToReceive := t.rxFrameLen - len(t.rxBuffer)
	if len(f.Data) > bytesToReceive {
		t.rxBuffer = append(t.rxBuffer, f.Data[:bytesToReceive]...)
	} else {
		t.rxBuffer = append(t.rxBuffer, f.Data...)
	}

	if len(t.rxBuffer) >= t.rxFrameLen {
		completedData := make([]byte, len(t.rxBuffer))
		copy(completedData, t.rxBuffer)
		select {
		case t.rxDataChan <- completedData:
		default:
			t.fireError(ErrReceiveQueueFull)
		}
		t.stopReceiving()
	} else {
		t.rxBlockCounter++
		if t.config.BlockSize > 0 && t.rxBlockCounter >= t.config.BlockSize {
			t.rxBlockCounter = 0
			t.sendFlowControl(ctx, FlowStatusContinueToSend, txChan)
			t.resetRxTimer()
		}
	}
}

func (t *Transport) resetRxTimer() {
	if !t.timerRxCF.Stop() {
		select {
		case <-t.timerRxCF.C:
		default:
		}
	}
	t.timerRxCF.Reset(t.config.TimeoutN_Cr)
}

func (t *Transport) sendFlowControl(ctx context.Context, status FlowStatus, txChan chan<- CanMessage) {
	payload := createFlowControlPayload(status, t.config.BlockSize, t.config.StMin)
	msg := t.makeTxMsgWithAddr(t.address, payload)
	if !sendCanMessage(ctx, txChan, msg) {
		t.fireError(ctx.Err())
	}
}
