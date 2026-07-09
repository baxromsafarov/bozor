package ws

import (
	"encoding/json"
	"time"

	"bozor/services/chat/internal/domain"
)

// Типы кадров WebSocket (поле "type" в server→client).
const (
	frameMessage = "message" // новое сообщение диалога
	frameAck     = "ack"     // подтверждение отправки собственного сообщения
	frameError   = "error"   // ошибка обработки клиентского кадра
)

// inbound — кадр client→server: отправка сообщения в диалог.
type inbound struct {
	ConversationID string `json:"conversation_id"`
	Body           string `json:"body"`
}

// outMessage — кадр server→client с новым сообщением.
type outMessage struct {
	Type           string `json:"type"`
	ID             string `json:"id"`
	ConversationID string `json:"conversation_id"`
	SenderID       string `json:"sender_id"`
	Body           string `json:"body"`
	CreatedAt      string `json:"created_at"`
}

// outAck — кадр server→client: сообщение принято и сохранено.
type outAck struct {
	Type           string `json:"type"`
	ID             string `json:"id"`
	ConversationID string `json:"conversation_id"`
	CreatedAt      string `json:"created_at"`
}

// outError — кадр server→client об ошибке обработки.
type outError struct {
	Type    string `json:"type"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// messageFrame сериализует сообщение в кадр доставки получателю.
func messageFrame(m domain.Message) []byte {
	b, _ := json.Marshal(outMessage{
		Type: frameMessage, ID: m.ID, ConversationID: m.ConversationID,
		SenderID: m.SenderID, Body: m.Body, CreatedAt: m.CreatedAt.UTC().Format(time.RFC3339),
	})
	return b
}

// ackFrame сериализует подтверждение отправителю.
func ackFrame(m domain.Message) []byte {
	b, _ := json.Marshal(outAck{
		Type: frameAck, ID: m.ID, ConversationID: m.ConversationID,
		CreatedAt: m.CreatedAt.UTC().Format(time.RFC3339),
	})
	return b
}

// errorFrame сериализует ошибку обработки клиентского кадра.
func errorFrame(code, message string) []byte {
	b, _ := json.Marshal(outError{Type: frameError, Code: code, Message: message})
	return b
}
