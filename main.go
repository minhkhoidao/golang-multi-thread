package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"time"
)

type VoiceContext struct {
	TextResponse string        `json:"text_response"`
	Data         []ContextData `json:"data"`
	Action       string        `json:"action"`
	Transcript   string        `json:"transcript"`
	NotiData     NotiData      `json:"notiData"`
	IsReadNoti   string        `json:"is_read_noti" default:"false"`
}

type ContextData struct {
	ProductName     string `json:"product_name"`
	SkuId           string `json:"sku"`
	AvailableToSale int32  `json:"ats_quantity"`
	ChangeQty       int32  `json:"change_quantity"`
	CartQty         int32  `json:"cart_quantity"`
	MinCartQty      int32  `json:"min_quantity"`
	MaxCartQty      int32  `json:"max_quantity"`
}

type NotiData struct {
	NotificationTime int64 `json:"notification_time"`
	ExpireTime       int64 `json:"expireTime"`
}

type Product struct {
	ProductName string `json:"product_name"`
	Sku         string `json:"sku"`
}

type AiSearchProductNameReq struct {
	ProductName string    `json:"product_name"`
	ListProduct []Product `json:"list_products"`
}
type ActionDto struct {
	Data []AiSearchProductNameReq `json:"data"`
}

type Message struct {
	Msg        []byte
	End        string
	session    string
	customerId string
}

type Server struct {
	listenAddr string
	ln         net.Listener
	quitch     chan struct{}
	msgch      chan Message
}

func NewServer(listenAddr string) *Server {
	return &Server{
		listenAddr: listenAddr,
		quitch:     make(chan struct{}),
		msgch:      make(chan Message, 100),
	}
}

func (s *Server) Start() error {
	ln, err := net.Listen("tcp", s.listenAddr)
	if err != nil {
		return err
	}
	go s.acceptConnect()

	defer ln.Close()
	s.ln = ln
	<-s.quitch
	close(s.msgch)
	return nil
}

func (s *Server) acceptConnect() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			fmt.Println("accept error:", err)
			continue
		}
		fmt.Println("Client connected:", conn.RemoteAddr().String())

		go s.handleClient(conn)
	}
}

func (s *Server) handleClient(conn net.Conn) {
	defer conn.Close()
	buf := make([]byte, 1024)
	var customerId, session string

	for {
		n, err := conn.Read(buf)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				fmt.Println("Client timed out, disconnecting:", conn.RemoteAddr().String())
				return
			}
			if err == io.EOF || err == net.ErrClosed {
				log.Printf("Client disconnected: %v", err)
				return
			}
			log.Printf("Unexpected read error: %v", err)
			return
		}

		data := buf[:n]

		if strings.Contains(string(data), "session:") {
			log.Println("Session message received")
			sessionData := strings.SplitN(string(data), "session:", 2)[1]
			session = strings.TrimSpace(sessionData)
			customerId = strings.Split(session, "-")[0]
			s.msgch <- Message{customerId: customerId, session: session}
			continue
		}

		if strings.Contains(string(data), "END") {
			log.Println("Received END message")
			s.msgch <- Message{End: "END", session: session, customerId: customerId}
			break
		}

		s.msgch <- Message{Msg: data, customerId: customerId, session: session}

	}
	dataContext := VoiceContext{
		TextResponse: "Hiện chỉ còn 3 thùng 24 lon coca 340ml, anh lấy tạm trước nhé, hàng về thêm em sẽ báo anh ạ",
		Data: []ContextData{
			{
				ProductName:     "Thùng 24 lon coca 340ml",
				SkuId:           "1234567890",
				AvailableToSale: 3,
				ChangeQty:       0,
				CartQty:         2,
				MinCartQty:      0,
				MaxCartQty:      7,
			},
		},
		Action:     "UPDATE_CART",
		Transcript: "Mình muốn mua coca",
	}
	log.Println("Sending response to client", dataContext)
	if err := responseToClient(conn, dataContext); err != nil {
		log.Printf("Error responding to client: %v", err)
	}
}

func responseToClient(conn net.Conn, data VoiceContext) error {
	response := map[string]any{
		"audioUrl":      "https://api.stg.telio.me/zss/v1/tts/stream?q=Coca+320ml+gi%C3%A1+174000+%C4%91%E1%BB%93ng+1+th%C3%B9ng.+Ch%E1%BB%8B+mu%E1%BB%91n+%C4%91%E1%BA%B7t+mua+lu%C3%B4n+ko+%E1%BA%A1%3F",
		"action":        data.Action,
		"transcript":    data.Transcript,
		"text_response": data.TextResponse,
	}
	log.Println("Response to client:", response)
	return json.NewEncoder(conn).Encode(response)
}

func (s *Server) sendFile(isSent bool, conn net.Conn, msg Message) error {
	log.Println("session gi day???", msg.session)

	if !isSent && msg.session != "" {
		_, err := conn.Write([]byte(fmt.Sprintf("session:%s\n", msg.session)))
		if err != nil {
			log.Printf("Error sending session message: %v", err)
			return err
		}
		_, err = conn.Write([]byte("name_api:voice_to_action\n"))
		if err != nil {
			log.Printf("Error sending name_api message: %v", err)
			return err
		}
		log.Println("Session message sent")
		isSent = true
	}

	_, err := conn.Write(msg.Msg)
	if err != nil {
		if strings.Contains(err.Error(), "broken pipe") {
			log.Println("Broken pipe error, attempting to reconnect...")
			return err
		}
		log.Printf("Error sending data message: %v", err)
		return err
	}

	if msg.End == "END" {
		_, err := conn.Write([]byte("END1"))
		if err != nil {
			log.Printf("Error sending END1 message: %v", err)
			return err
		}
		data := AiSearchProductNameReq{
			ProductName: "coca",
			ListProduct: []Product{
				{ProductName: "Bánh Custas kem trứng - Hộp x 20 bánh (460g)", Sku: "VN-FM-HXJ-782-001"},
				{ProductName: "Bánh Chocopie - Hộp x 20 bánh (600g)", Sku: "VN-FM-PAJ-104-001"},
			},
		}

		context, err := json.Marshal(data)
		if err != nil {
			log.Printf("JSON marshaling error: %v", err)
			return err
		}
		_, err = conn.Write(context)
		if err != nil {
			log.Printf("Error sending data message: %v", err)
			return err
		}

		_, err = conn.Write([]byte("END2"))
		if err != nil {
			log.Printf("Error sending END2 message: %v", err)
			return err
		}
	}

	return nil
}

func main() {
	server := NewServer(":8087")
	go func() {
		for {
			conn, err := net.Dial("tcp", "192.168.80.154:8011")
			if err != nil {
				log.Println("Error dialing:", err)
				time.Sleep(time.Second) // Wait before retrying
				continue                // Retry connection
			}
			isSent := false
			for msg := range server.msgch {
				err := server.sendFile(isSent, conn, msg)
				if err != nil {
					if strings.Contains(err.Error(), "broken pipe") {
						log.Println("Broken pipe error, reconnecting...")
					} else {
						log.Println("Send file error:", err)
					}
					conn.Close()
					break // Exit the loop and reconnect
				}
			}
			conn.Close()
		}
	}()

	server.Start()
}
