package realtime

import (
	"encoding/json"
	"sync"

	"github.com/coder/websocket"
)

var opcodeByType = map[string]byte{
	msgTypeSyncStep1: 1,
	msgTypeSyncStep2: 2,
	msgTypeUpdate:    3,
	msgTypeAwareness: 4,
}

var typeByOpcode = map[byte]string{
	1: msgTypeSyncStep1,
	2: msgTypeSyncStep2,
	3: msgTypeUpdate,
	4: msgTypeAwareness,
}

type Outbound struct {
	msg      collabMessage
	senderID string
	jsonOnce sync.Once
	jsonData []byte
	binOnce  sync.Once
	binData  []byte
}

func newOutbound(msg collabMessage, senderID string) *Outbound {
	return &Outbound{msg: msg, senderID: senderID}
}

func (o *Outbound) encode(binary bool) (websocket.MessageType, []byte) {
	if opcode, ok := opcodeByType[o.msg.Type]; ok && binary {
		o.binOnce.Do(func() {
			frame := make([]byte, 0, 2+len(o.senderID)+len(o.msg.Data))
			frame = append(frame, opcode, byte(len(o.senderID)))
			frame = append(frame, o.senderID...)
			o.binData = append(frame, o.msg.Data...)
		})
		return websocket.MessageBinary, o.binData
	}
	o.jsonOnce.Do(func() {
		o.jsonData, _ = json.Marshal(o.msg)
	})
	return websocket.MessageText, o.jsonData
}

func decodeBinaryFrame(data []byte) (collabMessage, bool) {
	if len(data) < 1 {
		return collabMessage{}, false
	}
	msgType, ok := typeByOpcode[data[0]]
	if !ok {
		return collabMessage{}, false
	}
	return collabMessage{Type: msgType, Data: data[1:]}, true
}
