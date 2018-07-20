# Go chat server 

Telnet demo:
![](https://raw.github.com/jamischarles/github-org-browser/master/screenshots/go_server_demo_telnet.gif)
Curl demo:  
![](https://raw.github.com/jamischarles/github-org-browser/master/screenshots/go_server_demo_curl.gif)

## Start instructions
### To run the chat server:
`$ go run *.go`

### To connect a telnet client:
`telnet localhost 3333`

### To post messages via curl:
`curl -d 'username=myFancyUserName&msg=How do you do fellow kids?' 'http://localhost:3000/post'`

### To check messages history via curl:
`curl 'http://localhost:3000/messages'`


## Notes:
There aren't any bugs I'm aware of. There is currently only 1 big chatroom. I'm using channels to broadcast messages between all the connected clients. After you connect via telnet you can type `/help` to se what commands are available.

All chat messages are saved to `./messages.log`.

Given more time, I would add tests, add proper error conditions, and ensure I'm closing the channels and connections properly. Following that, I'd start testing scale, and see how the server scales before optimizing.  
