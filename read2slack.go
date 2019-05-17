package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/BurntSushi/toml"
	"github.com/lunixbochs/vtclean"
	flag "github.com/spf13/pflag"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/user"
	"strconv"
	"strings"
	"time"
)

const (
	// 1 msg is less than 4000 chars
	SlackCharLimit = 4000
	//1 msg per 2 second
	SlackMsgPerSecond = 2
	SlackChannel      = "chatops"
)

type TomlConfig struct {
	Title    string
	User     slackUserInfo
	Channels map[string]channel
}

type slackUserInfo struct {
	Username  string `toml:"name"`
	IconEmoji string `toml:"icon"`
	Default   string `toml:"default_channel"`
}

type channel struct {
	HookUrl string `toml:"url"`
	Channel string
}

func ReadTomlConfig() (*TomlConfig, error) {
	homeDir := ""
	usr, err := user.Current()
	if err == nil {
		homeDir = usr.HomeDir
	}

	for _, path := range []string{"/etc/slackchannels.toml", homeDir + "/.slackchannels.toml", "./slackchannels.toml"} {
		_, err := os.Open(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}

		var conf TomlConfig

		if _, err := toml.DecodeFile(path, &conf); err != nil {
			return nil, err
		}

		return &conf, nil
	}
	return nil, errors.New("Config file not found")
}

type SlackMsg struct {
	Channel   string `json:"channel"`
	Username  string `json:"username,omitempty"`
	Text      string `json:"text"`
	Parse     string `json:"parse"`
	IconEmoji string `json:"icon_emoji,omitempty"`
}

func (m SlackMsg) Encode() (string, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (m SlackMsg) Post(WebhookURL string) (error, int) {
	encoded, err := m.Encode()
	if err != nil {
		return err, http.StatusExpectationFailed //this is bad
	}

	resp, err := http.PostForm(WebhookURL, url.Values{"payload": {encoded}})

	if err != nil {
		if resp.StatusCode == http.StatusTooManyRequests {
			fmt.Println("in out Post")
			err = rateLimitDelay(resp)
			if err != nil {
				return err, http.StatusTooManyRequests
			}
		}

		return err, resp.StatusCode
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		err = rateLimitDelay(resp)
		if err != nil {
			return err, http.StatusTooManyRequests
		}

		return nil, http.StatusTooManyRequests
	} else if resp.StatusCode != http.StatusOK {
		return errors.New("Not OK"), resp.StatusCode
	}

	return nil, resp.StatusCode
}

func (m SlackMsg) PostToSlack(hookUrl string) {

	for {
		err, status := m.Post(hookUrl)
		if err != nil {
			if status == http.StatusTooManyRequests {
				continue
			} else if status == http.StatusInternalServerError {
				time.Sleep(time.Duration(60) * time.Second)
				continue
			} else {
				log.Fatalf("Post failed: %v, status: %d", err, status)
			}
		}
		break
	}
}

func (m SlackMsg) PostToSlackBigMsg(hookUrl, text string) {
	slackRateLimit := time.Duration(SlackMsgPerSecond) * time.Second

	//break the string into multiple of SlackCharLimit
	stringSlice := []rune(text)
	msg := ""

	for i, r := range stringSlice {
		msg = msg + string(r)

		if i > 0 && (i+1)%SlackCharLimit == 0 {
			m.Text = msg
			time.Sleep(time.Duration(slackRateLimit))
			m.PostToSlack(hookUrl)
		}
	}

	if len(msg) > 0 {
		m.Text = msg
		time.Sleep(time.Duration(slackRateLimit))
		m.PostToSlack(hookUrl)
	}
}

func rateLimitDelay(resp *http.Response) error {
	sleepStr := resp.Header.Get("Retry-After")
	if sleepStr != "" {
		sec, err := strconv.Atoi(sleepStr)

		if err != nil {
			return errors.New("Failed to get the timeout value")
		}

		time.Sleep(time.Duration(sec) * time.Second)
	} else {
		time.Sleep(60 * time.Second)
	}

	return nil
}

func username() string {
	username := "<unknown>"
	usr, err := user.Current()
	if err == nil {
		username = usr.Username
	}

	hostname := "<unknown>"
	host, err := os.Hostname()
	if err == nil {
		hostname = host
	}
	return fmt.Sprintf("%s@%s", username, hostname)
}

func scanner(stdin io.Reader, out chan<- string) {

	scanner := bufio.NewScanner(stdin)
	for scanner.Scan() {
		// strip out ansi color codes
		text := vtclean.Clean(scanner.Text(), false)
		text += "\n"
		fmt.Print(text)

		out <- text // send the string to the channel
	}
	fmt.Println("closing scanner")
	close(out) // close channel

	if err := scanner.Err(); err != nil {
		log.Fatalf("Error reading: %v", err)
	}
}

func poster(cfg *TomlConfig, in <-chan string, done chan bool) {

	name := username()
	if cfg.User.Username != "" {
		name = cfg.User.Username
	}

	icon := ""
	if cfg.User.IconEmoji != "" {
		icon = cfg.User.IconEmoji
	}

	var ch channel
	var err bool

	if cfg.User.Default != "" {
		// if default channel specified
		ch, err = cfg.Channels[cfg.User.Default]

		if !err {
			fmt.Printf("Could not find channel %s in config file (default)\n", cfg.User.Default)
			done <- true
			return
		} else if ch.Channel == "" || ch.HookUrl == "" {
			fmt.Printf("Missing information for %s\n", cfg.User.Default)
			done <- true
			return
		}

	} else {
		ch, err = cfg.Channels[SlackChannel]

		if !err {
			fmt.Printf("Could not find channel %s in config file (non default)\n", SlackChannel)
			done <- true
			return
		} else if ch.Channel == "" || ch.HookUrl == "" {
			fmt.Printf("Missing information for %s\n", SlackChannel)
			done <- true
			return
		}
	}

	slackRateLimit := time.Duration(SlackMsgPerSecond) * time.Second
	startTime := time.Now()
	var buffer bytes.Buffer
	msgSize := 0
	msg := SlackMsg{
		Channel:   ch.Channel,
		Username:  name,
		Parse:     "full",
		Text:      "",
		IconEmoji: icon,
	}

	for {
		select {
		case text, ok := <-in:
			if !ok {
				if buffer.Len() > 0 {
					//fmt.Println("some data left to be sent")
					msg.Text = buffer.String()
					msg.PostToSlack(ch.HookUrl)
				}

				done <- true
			}

			duration := time.Since(startTime)
			textLen := len(text)

			if textLen > SlackCharLimit {
				// sleep until end of slackRateLimit
				time.Sleep(time.Duration(slackRateLimit - duration))

				if buffer.Len() > 0 {
					// post the mesg in buffer
					msg.Text = buffer.String()
					msg.PostToSlack(ch.HookUrl) // post whatever in buffer to slack
				}

				msg.Text = ""
				msg.PostToSlackBigMsg(ch.HookUrl, text)

				startTime = time.Now()
				buffer.Reset()
				msgSize = 0

			} else if msgSize+textLen > SlackCharLimit {
				// sleep until end of slackRateLimit
				time.Sleep(time.Duration(slackRateLimit - duration))

				if buffer.Len() > 0 {
					msg.Text = buffer.String()
					msg.PostToSlack(ch.HookUrl)

					startTime = time.Now()
					buffer.Reset()
					msgSize = 0
				}
			}

			//add this text to msg
			n, err := buffer.WriteString(text)
			if err != nil {
				log.Fatalf("Buffer for output becomes too large")
			}

			if n == textLen {
				msgSize += n
			}

		default:
			duration := time.Since(startTime)

			if duration.Nanoseconds() >= slackRateLimit.Nanoseconds() {
				//fmt.Println("rate limit expires")

				if buffer.Len() > 0 {
					msg.Text = buffer.String()
					msg.PostToSlack(ch.HookUrl)

					startTime = time.Now()
					buffer.Reset()
					msgSize = 0
				}
			}
		}

	} // end for loop

	if buffer.Len() > 0 {
		//fmt.Println("some data left to be sent")
		msg.Text = buffer.String()
		msg.PostToSlack(ch.HookUrl)
	}

	done <- true
}

func main() {

	cfg, err := ReadTomlConfig()
	if err != nil {
		log.Fatalf("Could not read config: %v", err)
	}

	// By default use "user@server", unless overridden by cfg.User.name
	// can be "", implying Slack should use the default username, so we have
	// to check if the value was set, not just for a non-empty string.
	defaultName := username()
	if cfg.User.Username != "" {
		defaultName = cfg.User.Username
	}

	defaultIcon := ""

	if cfg.User.IconEmoji != "" {
		defaultIcon = cfg.User.IconEmoji
	}
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: read2slack [-c channel] [-n name] [-i icon] [message]")
	}

	name := flag.StringP("name", "n", defaultName, "name")
	icon := flag.StringP("icon", "i", defaultIcon, "icon")

	//if not specified than constant SlackChannel value is used
	c := flag.StringP("channel", "c", SlackChannel, "channel")
	flag.Parse()

	var ch channel

	if cfg.User.Default != "" {
		ch, ok := cfg.Channels[cfg.User.Default]

		if !ok {
			fmt.Printf("Could not find channel %s info in config file (main)\n", cfg.User.Default)
			return
		} else if ch.Channel == "" || ch.HookUrl == "" {
			fmt.Printf("Missing information for %s\n", cfg.User.Default)
			return
		}
	} else {

		ch, ok := cfg.Channels[*c]
		if !ok {
			fmt.Printf("Could not find default channel %s info in config file (main)\n", *c)
			return
		} else if ch.Channel == "" || ch.HookUrl == "" {
			fmt.Printf("Missing information for %s\n", *c)
			return
		}

		//set the Default channel to cmd line
		cfg.User.Default = *c
	}

	// using the command directly with string inputs
	args := flag.Args()
	if len(args) > 0 {
		msg := SlackMsg{
			Channel:   ch.Channel,
			Username:  *name,
			Parse:     "full",
			Text:      "",
			IconEmoji: *icon,
		}

		text := strings.Join(args, " ")

		if len(text) > SlackCharLimit {
			msg.PostToSlackBigMsg(ch.HookUrl, text)
		} else {
			msg.Text = text
			msg.PostToSlack(ch.HookUrl)
		}

		return
	}
	in := make(chan string) // unbuffered channel for string comm.
	done := make(chan bool) // unbuffered channel to indicate end

	go scanner(os.Stdin, in)
	go poster(cfg, in, done)

	<-done //waiting for poster to finish
}
