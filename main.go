package main

import (
	"encoding/base64"
	"encoding/csv"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/bwmarrin/discordgo"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/gmail/v1"
)

var (
	token             string
	groupID           string
	usernameToUser    map[string]*discordgo.User
	discordToMichigan map[string]string
)

func getUsers(dg *discordgo.Session) {
	members, err := dg.GuildMembers(groupID, "0", 1000)
	if err != nil {
		fmt.Println("error getting members", err)
	}

	for i := range members {
		usernameToUser[members[i].User.String()] = members[i].User
	}
}

func getRoles(dg *discordgo.Session) discordgo.Roles {
	roles, err := dg.GuildRoles(groupID)
	if err != nil {
		fmt.Println("error getting roles", err)
	}
	return roles
}

func getVerifiedRoleID(dg *discordgo.Session) *discordgo.Role {
	roles := getRoles(dg)
	for i := range roles {
		if roles[i].Name == "Verified" {
			return roles[i]
		}
	}
	return nil
}

func checkEmail(srv *gmail.Service) {
	user := "me"

	r, err := srv.Users.Messages.List(user).Do()
	if err != nil {
		log.Println("Unable to retrieve messages: %v", err)
		return
	}

	for _, l := range r.Messages {
		msg, err := srv.Users.Messages.Get(user, l.Id).Do()
		if err != nil {
			fmt.Println("error getting messages")
		}
		if len(msg.Payload.Headers) < 20 {
			fmt.Println("malformed email", err)
			continue
		}
		fromAddress := msg.Payload.Headers[6].Value
		subject := msg.Payload.Headers[20].Value

		fromAddress = fromAddress[1 : len(fromAddress)-1]
		michiganID := strings.Split(fromAddress, "@")[0]
		if match, _ := regexp.MatchString(`.*\@umich\.edu`, fromAddress); match {
			// is a umich email
			data := msg.Payload.Parts[0].Body.Data
			body, _ := base64.StdEncoding.DecodeString(data)

			discordUsername := parseEmail(string(subject), string(body))
			fmt.Printf("%s was verified (%s)\n", discordUsername, michiganID)
			discordToMichigan[discordUsername] = michiganID
		}
		srv.Users.Messages.Trash(user, l.Id).Do()
	}
}

func getVerifyChannel(dg *discordgo.Session) *discordgo.Channel {
	channels, _ := dg.GuildChannels(groupID)
	for i := range channels {
		if channels[i].Name == "verify" {
			return channels[i]
		}
	}
	return nil
}

func getInternalVerifyChannel(dg *discordgo.Session) *discordgo.Channel {
	channels, _ := dg.GuildChannels(groupID)
	for i := range channels {
		if channels[i].Name == "internal-verify" {
			return channels[i]
		}
	}
	return nil
}

func applyRoles(dg *discordgo.Session) {
	for disc := range discordToMichigan {
		user, ok := usernameToUser[disc]
		if !ok {
			fmt.Printf("could not find username %s in server\n", disc)
			delete(discordToMichigan, disc)
			return
		}
		dg.GuildMemberRoleAdd(groupID, user.ID, getVerifiedRoleID(dg).ID)
		msg := discordgo.MessageSend{}
		msg.Content = fmt.Sprintf("%s was verified!", user.Mention())
		dg.ChannelMessageSendComplex(getVerifyChannel(dg).ID, &msg)
		dg.ChannelMessageSend(getInternalVerifyChannel(dg).ID, fmt.Sprintf("Verified %s. (%s)", disc, discordToMichigan[disc]))
		delete(discordToMichigan, disc)
	}
}

func parseEmail(subject string, body string) string {
	discordUsernameRe := regexp.MustCompile(`.*\S#[0-9]{4}`)
	var username string
	username = discordUsernameRe.FindString(subject + "\n" + body)
	return username
}

func saveMapToCSV(file *os.File, maptosave map[string]string) {
	writer := csv.NewWriter(file)
	defer writer.Flush()

	for key, val := range maptosave {
		err := writer.Write([]string{key, val})
		if err != nil {
			fmt.Println("error writing csv", err)
		}
	}
}

func main() {
	flag.StringVar(&token, "t", "", "Bot Token")
	flag.StringVar(&groupID, "g", "", "Group ID")
	flag.Parse()

	usernameToUser = make(map[string]*discordgo.User, 0)
	discordToMichigan = make(map[string]string, 0)

	file, err := os.OpenFile("users.csv", os.O_APPEND|os.O_WRONLY, 0600)
	defer file.Close()
	if err != nil {
		fmt.Println("error writing csv", err)
		return
	}

	dg, err := discordgo.New("Bot " + token)

	if err != nil {
		fmt.Println("error creating Discord session,", err)
		return
	}

	err = dg.Open()
	if err != nil {
		fmt.Println("error opening connection,", err)
		return
	}
	b, err := ioutil.ReadFile("client_secret.json")
	if err != nil {
		log.Fatalf("Unable to read client secret file: %v", err)
	}

	config, err := google.ConfigFromJSON(b, gmail.GmailModifyScope)
	if err != nil {
		log.Fatalf("Unable to parse client secret file to config: %v", err)
	}

	srv, err := gmail.New(getClient(config))
	if err != nil {
		log.Fatalf("Unable to retrieve Gmail client: %v", err)
	}

	fmt.Println("WolverineBot is running. Press CTRL-C to exit.")

	go func() {
		for range time.Tick(time.Second * 5) {
			getUsers(dg)
			getVerifiedRoleID(dg)
			checkEmail(srv)
			saveMapToCSV(file, discordToMichigan)
			applyRoles(dg)
		}
	}()

	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt, os.Kill)

	<-sc
	dg.Close()
}
