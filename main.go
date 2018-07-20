package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

/*
TODO:
- send message to all the people... (channels?) - DONE
	- Don't subscribe to your own messages...
- store some global state
	- for id, name etc
- don't send messages that you sent...

- allow override from a config file...
- implement telnet protocol ( try manually first, then use package)
- add proper err handling
- make sure I'm following Go style

- tests?

*/

// FIXME: move somewhere else...
type ChatMessage struct {
	msg string
	id  int // FIXME: change this type?
}

// TODO: move all the broker stuff into a separate file...?
type Broker struct {
	stopCh    chan struct{}
	publishCh chan interface{}
	subCh     chan chan interface{}
	unsubCh   chan chan interface{}
}

func NewBroker() *Broker {
	return &Broker{
		stopCh:    make(chan struct{}),
		publishCh: make(chan interface{}, 1),
		subCh:     make(chan chan interface{}, 1),
		unsubCh:   make(chan chan interface{}, 1),
	}
}

func (b *Broker) Start() {
	subs := map[chan interface{}]struct{}{}
	for {
		select {
		case <-b.stopCh:
			return
		case msgCh := <-b.subCh:
			subs[msgCh] = struct{}{}
		case msgCh := <-b.unsubCh:
			delete(subs, msgCh)
		case msg := <-b.publishCh:
			for msgCh := range subs {
				// msgCh is buffered, use non-blocking send to protect the broker:
				select {
				case msgCh <- msg:
				default:
				}
			}
		}
	}
}

func (b *Broker) Stop() {
	close(b.stopCh)
}

func (b *Broker) Subscribe() chan interface{} {
	msgCh := make(chan interface{}, 5)
	b.subCh <- msgCh
	return msgCh
}

func (b *Broker) Unsubscribe(msgCh chan interface{}) {
	b.unsubCh <- msgCh
}

func (b *Broker) Publish(msg interface{}) {
	b.publishCh <- msg
}

// END broker file?

// const (
// 	CONN_HOST = "localhost"
// 	CONN_PORT = "3333"
// 	CONN_TYPE = "tcp"
// )

type User struct {
	id       int
	name     string
	muteList map[int]string //users to mute by id
}

type HttpAPI struct {
	b   *Broker
	cfg Config
}

// global var. FIXME: good or bad?
// FIXME: just change this to array?
// var state = map[int]string{0: "System"} // init empty map
// var state = map[int]map[string]string{} // init empty map
var state = []User{}
var userNameList = map[string]int{} // map userNames to user ID
var msgHistory []string             // keep last 100 messages in mem

// state = append(state, systemUser)

// var state = map[int]map[string]string{0: {"name": "System", "mute":[]} } // init empty map

func main() {

	addNewUser("system")

	cfg := readConfigFromFile()

	// create and start the chat message broker
	// create one of these for each room?
	b := NewBroker()
	go b.Start()

	api := &HttpAPI{cfg: cfg, b: b}

	// just use different port?
	// anon function as goroutine
	go func() {
		http.HandleFunc("/messages", httpHistoryHandler)
		http.HandleFunc("/post", api.httpPosthandler)
		log.Fatal(http.ListenAndServe(":3000", nil))
	}()

	// Listen for incoming connections.
	ln, err := net.Listen(cfg.ConnType, cfg.Host+":"+cfg.Port)

	if err != nil {
		fmt.Println("Error listening:", err.Error())
		os.Exit(1)
	}

	defer ln.Close()

	// FIXME: move this into separate fn
	// I think this runs until all the goroutines close
	for {
		conn, err := ln.Accept()
		if err != nil {
			fmt.Println("Error handling connection:", err.Error())
			// handle error
		}

		id := addNewUser("")

		go handleConnection(conn, b, id, cfg)
	}

}

func httpHistoryHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "Here are the most recent messages: \n%s", getMsgHistory())
}
func (api *HttpAPI) httpPosthandler(w http.ResponseWriter, r *http.Request) {
	var userId int
	// body: pass username, msgbody
	r.ParseForm()

	// FIXME: check for params, and respond with error if some are missing
	username := r.Form["username"][0]
	msg := r.Form["msg"][0]

	// FIXME: get the user, or create a new one with that name ...
	// get user by name and if it doesn't exist, create it...
	// make them use their email address
	// TODO: add proper authentication
	usernameExists := hasUserNameBeenTaken(username)
	if usernameExists == true {
		userId = userNameList[username]
	} else {
		userId = addNewUser(username)
	}

	// auto-increment this person...
	sendMessage(msg, userId, api.b, api.cfg)

	fmt.Fprintf(w, "Message successfully posted: \n%s", msgHistory[len(msgHistory)-1])

	// fmt.Fprintf(w, "Here are the most recent messages: \n%s", getMsgHistory())
}

// Create and subscribe 3 clients:
// FIXME: change this name...
func clientFunc(id int, b *Broker, conn net.Conn) {
	msgCh := b.Subscribe()
	for {
		// FIXME: can we change the return type to a ChatMessage instead of interface?
		msgData := <-msgCh
		msgDetails := msgData.(ChatMessage)

		// don't write message from self, or if author was muted
		if id != msgDetails.id && isMessageAuthorMuted(id, msgDetails.id) == false {
			conn.Write([]byte(msgDetails.msg))
		}

	}
}

// FIXME: make config global?
func handleConnection(conn net.Conn, b *Broker, id int, cfg Config) {
	remoteAddr := conn.RemoteAddr().String()
	fmt.Printf("Client id: %d with username [%s] connected from %s\n", id, getUserName(id), remoteAddr)

	msg := fmt.Sprintf("%s has joined", getUserName(id))
	sendMessage(msg, 0, b, cfg)

	printWelcomeMessage(conn, id)

	// since we are turning this into a goroutine, it's kind of the same as non-blocking async stuff in node
	go clientFunc(id, b, conn)

	// subscribe to each message channel...
	scanner := bufio.NewScanner(conn)

	// scanner2 := bufio.NewScanner(queue)

	for {
		ok := scanner.Scan()

		msg := fmt.Sprintf(scanner.Text())

		// if msg starts with / it's a command
		if len(msg) > 0 && msg[0] == '/' {
			handleCommand(msg, conn, id, b, cfg)
		} else {
			sendMessage(msg, id, b, cfg)
		}

		if !ok {
			break
		}
	}

	fmt.Println("Client at " + remoteAddr + " disconnected.")
}

// utils... FIXME: Move this to a utils (after I read the formatting doc)
func getTimeStamp() (timestamp string) {
	now := time.Now()
	// FIXME: handle the error?
	// t1, _ := time.Parse(
	// 	time.RFC3339,
	// 	time.Now())
	//
	// return t1.String()

	// test, _ := time.Parse("15:04:05 MST", fmt.Sprintf("05:00:00 %s"))

	timestamp = fmt.Sprintf("%d:%d", now.Hour(),
		now.Minute())
	return timestamp
}

func printWelcomeMessage(conn net.Conn, id int) {
	conn.Write([]byte("> Welcome to the chat room. Type /help for a list of available commands.\n> Your username is [" + getUserName(id) + "]\n"))
}

func changeUserName(id int, newName string) {
	state[id].name = newName
	userNameList[newName] = id
}

func hasUserNameBeenTaken(name string) bool {
	if _, ok := userNameList[name]; ok {
		return true
	}
	return false
}

func getMuteList(id int) map[int]string {
	return state[id].muteList
}

// searches through mutelist of a user. Returns true if msg should be muted
func isMessageAuthorMuted(userIdRequestingMute int, messageAuthorId int) bool {

	muteList := getMuteList(userIdRequestingMute)

	if _, ok := muteList[messageAuthorId]; ok {
		return true
	}

	return false

}

// returns id of newest user added
func addNewUser(userName string) int {
	// newUser := User{name: userName, id: len(state), muteList: map[int]string{}}
	newUser := User{name: userName, id: len(state), muteList: map[int]string{}}
	state = append(state, newUser)

	// FIXME: add error state for dupes
	userNameList[newUser.name] = newUser.id

	return len(state) - 1
}

// look up username
func getUserName(id int) string {
	// FIXME: check for index out of range?
	userName := state[id].name

	if userName != "" {
		return userName
	}

	// FIXME: generate fun anon names
	userName = "Anon" + strconv.Itoa(id)
	return userName
}

// retrieve saved messages in history
func getMsgHistory() string {
	return strings.Join(msgHistory, "")
}

// add messages to history. limits history to 29 messages
// FIXME: Consider using channels in here?
func updateMsgHistory(msg string) {

	// remove first item, if message history is 30
	if len(msgHistory) > 29 {
		msgHistory = append(msgHistory[1:], msg)
	} else {
		msgHistory = append(msgHistory, msg)
	}

}

// func getConnectedUsers() []string {
// 	keys := make([]string, 0, len(state))
// 	for k := range state {
// 		keys = append(keys, k)
// 	}
//
// 	return strings.Join(keys)
// }

func getIdFromUserName(userName string) int {
	return userNameList[userName]
}

// adds list of users to mute as slice for user info...
func muteUser(requestorId int, userToMute string) {
	idToMute := getIdFromUserName(userToMute)
	state[requestorId].muteList[idToMute] = "" // hash map for quick and easy access. Don't care about the value
}

func unMuteUser(requestorId int, userToUnMute string) {
	idToUnMute := getIdFromUserName(userToUnMute)
	delete(state[requestorId].muteList, idToUnMute)
}

// adds the metadata to the message
func formatMessage(msg string, id int) string {
	// look up the user if their name has been set...

	formattedMsg := fmt.Sprintf("%s [%s] %s\n", getTimeStamp(), getUserName(id), msg)
	return formattedMsg
}

// format and send message to all connected clients
// sends messages to all the connected clients
func sendMessage(msg string, id int, b *Broker, cfg Config) {
	if msg == "" {
		return
	}
	msg = formatMessage(msg, id)
	msgData := ChatMessage{msg: msg, id: id}
	b.Publish(msgData)   // send to all users
	logMessage(msg, cfg) // log message
	updateMsgHistory(msg)
}

func logMessage(msg string, cfg Config) {
	fmt.Print(msg)
	saveLogToFile(msg, cfg.LogPath)
}

func saveLogToFile(msg string, logFilePath string) {
	f, err := os.OpenFile(logFilePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)

	if err != nil {
		panic(err)
	}

	defer f.Close()
	fmt.Fprintf(f, msg)
}

func handleCommand(message string, conn net.Conn, id int, b *Broker, cfg Config) {

	// if len(queue) > 0 {
	// 	// msg := <-queue
	// 	other := "[Jamis] " + <-queue + "\n"
	// 	conn.Write([]byte(other))
	// }

	if len(message) > 0 && message[0] == '/' {
		switch {

		case message == "/help":

			msg := `> Commands you can run:
/help - See commands available to you
/history - See the last 30 messages
/name - Change your username
/mute [name] - mute another user
/unmute [name] - unmute a user you have muted previously
/quit - disconnect your client` + "\n"
			// msg = formatMessage(msg, 0)
			conn.Write([]byte(msg))

		case message == "/history":
			resp := strings.Join(msgHistory, "")
			// fmt.Print("> History: \n" + resp)
			resp = "> Recent messages: \n" + resp
			conn.Write([]byte(resp))

		// starts with /name
		case strings.HasPrefix(message, "/name "):
			oldName := getUserName(id)
			newName := strings.Replace(message, "/name ", "", -1)

			nameTaken := hasUserNameBeenTaken(newName)

			// FIXME: add tests, return error message if taken, or if invalid...
			// FIXME: verify that name isn't taken yet
			// success / failure?
			if nameTaken == true {

				msg := "I'm sorry. The userName [" + newName + "] has already been taken. Try another name"
				msg = formatMessage(msg, 0) // system msg to 1 person only
				conn.Write([]byte(msg))

				return
			}

			changeUserName(id, newName)

			// success msg
			// send as system message
			msg := fmt.Sprintf("%s has changed their name to %s", oldName, newName)
			sendMessage(msg, 0, b, cfg) // send system notification

			// Success / failure FIXME:
			// conn.Write([]byte(resp))

		case strings.HasPrefix(message, "/mute "):
			userToMute := strings.Replace(message, "/mute ", "", -1)

			muteUser(id, userToMute)

			// success msg
			msg := fmt.Sprintf("%s has muted %s", getUserName(id), userToMute)
			sendMessage(msg, 0, b, cfg) // send system notification

		case strings.HasPrefix(message, "/unmute "):
			userToUnMute := strings.Replace(message, "/unmute ", "", -1)

			unMuteUser(id, userToUnMute)

			// success msg
			msg := fmt.Sprintf("%s has UN-muted %s", getUserName(id), userToUnMute)
			sendMessage(msg, 0, b, cfg) // send system notification
			// fmt.Println(state)

		// Success / failure FIXME:
		// conn.Write([]byte(resp))

		case message == "/quit":
			fmt.Println("Quitting.")
			msg := fmt.Sprintf("%s has left", getUserName(id))
			sendMessage(msg, 0, b, cfg)
			conn.Write([]byte("> Goodbye\n"))
			conn.Close()

		default:
			conn.Write([]byte("Unrecognized command.\n"))
		}
	}

}

type Config struct {
	Host     string `json:"host"`
	Port     string `json:"port"`
	ConnType string `json:"connType"`
	LogPath  string `json:"logFilePath"`
}

func readConfigFromFile() Config {

	byteVal, _ := ioutil.ReadFile("./config.json") // read the entire file into memory

	// map of arbitrary data types (we can prolly optimize this later...)
	// var data map[string]interface{}
	var cfg Config

	// decode json and store in data map. handle err
	if err := json.Unmarshal(byteVal, &cfg); err != nil {
		panic(err)
	}

	return cfg
}

// func handleConnection(conn net.Conn) {
// 	// Make a buffer to hold incoming data.
// 	buf := make([]byte, 1024)
// 	// Read the incoming connection into the buffer.
// 	reqLen, _ := conn.Read(buf)
//
// 	conn.Write(int(reqLen))
// 	conn.Write([]byte("Message received."))
// 	conn.Close()
//
// 	os.Exit(1)
// }
