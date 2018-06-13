package main

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"github.com/bwmarrin/discordgo"
	log "github.com/sirupsen/logrus"
	"golang.org/x/net/html/charset"
	"io/ioutil"
	"net/http"
	"runtime"
	"time"
)

const (
	channelID    = ""
	token        = ""
	wfurl        = ""
	webhookID    = ""
	webhookToken = ""
)

type WFRSS struct {
	XMLName xml.Name `xml:"rss"`
	Item    []WFItem `xml:"channel>item"`
}

type WFItem struct {
	Guid           string    `xml:"guid"`
	Title          string    `xml:"title"`
	Author         string    `xml:"author"`
	Description    string    `xml:"description"`
	PubDate        string    `xml:"pubDate"`
	ExpiryDate     string    `xml:"expiry"`
	PubDateTime    time.Time `xml:"-"`
	ExpiryDateTime time.Time `xml:"-"`
}

var msg string
var data chan *WFRSS
var ignoreExp map[string]bool = make(map[string]bool, 1)
var ignorePub map[string]bool = make(map[string]bool, 1)

func main() {
	runtime.GOMAXPROCS(runtime.NumCPU())

	done := make(chan bool)
	data = make(chan *WFRSS)
	dg, err := discordgo.New("Bot " + token)

	if err != nil {
		log.Fatalln("Error creating Discord session", err)
	}

	go discord(dg)
	go retrieveData(dg, data)
	go processData(dg, data)
	go notification(dg, data)

	dg.Close()
	<-done
}

func discord(dg *discordgo.Session) {
	dg.AddHandler(messageCreate)

	err := dg.Open()
	if err != nil {
		log.Errorln("Error opening connection", err)
		return
	}

	log.Infoln("Bot is now running. Press CTRL-C to exit.")
}

func retrieveData(dg *discordgo.Session, data chan<- *WFRSS) {
	resp, err := http.Get(wfurl)

	wf := &WFRSS{}
	if err != nil {
		log.Errorf("Cannot retrieve the content of %s\n", wfurl)
		log.Errorln(err)
	}

	respReader, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Errorln(err)
	}

	resp.Body.Close()
	err = parseXML(respReader, wf)
	if err != nil {
		log.Errorln(err)
	}

	err = parseDate(wf)
	if err != nil {
		log.Errorln(err)
	}

	data <- wf

	if data != nil {
		c := time.Tick(1 * time.Minute)
		for now := range c {
			log.Infoln("Retrieving infos of rss at: ", now)
			go retrieveData(dg, nil)
		}
	}
}

func processData(dg *discordgo.Session, data chan *WFRSS) {
	wf := <-data
	now := time.Now()

	expiryIndex, rateExp := nearestExpiryDate(wf.Item)
	pubIndex, ratePub := nearestPubDate(wf.Item)
	wfi := wf.Item[expiryIndex]
	log.Infoln(rateExp, wfi.ExpiryDateTime.String())

	log.Infoln("Processing rss")
	if ignoreExp[wfi.Guid] == false {
		if rateExp == 1.00 {
			msg = fmt.Sprintf("**Titulo**: %s\n**Alerta expirado!**", wfi.Title)
			dg.WebhookExecute(webhookID, webhookToken, false, &discordgo.WebhookParams{
				Content: msg,
			})
			wf.Item = wf.Item[expiryIndex+1:]
			ignoreExp[wfi.Guid] = true
		}
	}

	wfi = wf.Item[pubIndex]
	subDateExpiry := wfi.ExpiryDateTime.Sub(now)
	//subDatePub := wfi.PubDateTime.Sub(now)

	msg = fmt.Sprintf("**Titulo:** %s\n**Expira em:** %dm\n**Tipo:** %s\n", wfi.Title, int(subDateExpiry.Minutes()), wfi.Author)
	if ignorePub[wfi.Guid] == false {
		if ratePub == 1.00 {
			log.Info("Sending content to webhook")
			dg.WebhookExecute(webhookID, webhookToken, false, &discordgo.WebhookParams{
				Content: msg,
			})
			ignorePub[wfi.Guid] = true
		}
	}
	data <- wf
}

func notification(dg *discordgo.Session, data chan *WFRSS) {
	c := time.Tick(200 * time.Millisecond)
	for now := range c {
		log.Info("Processing notification at: ", now)
		go processData(dg, data)
	}
}

func parseXML(xmlDoc []byte, target interface{}) error {
	reader := bytes.NewReader(xmlDoc)
	decoder := xml.NewDecoder(reader)
	decoder.CharsetReader = charset.NewReaderLabel

	if err := decoder.Decode(target); err != nil {
		return err
	}
	return nil
}

func parseDate(wf *WFRSS) error {
	var err error

	for i, _ := range wf.Item {
		err = strDateToTime(&wf.Item[i])
		if err != nil {
			return err
		}
	}
	return nil
}

func strDateToTime(wf *WFItem) error {
	var err error

	if wf.ExpiryDate != "" {
		wf.PubDateTime, err = time.Parse(time.RFC1123Z, wf.PubDate)
		wf.ExpiryDateTime, err = time.Parse(time.RFC1123Z, wf.ExpiryDate)
		wf.PubDateTime = wf.PubDateTime.Local()
		wf.ExpiryDateTime = wf.ExpiryDateTime.Local()

		if err != nil {
			return err
		}
	}
	return nil
}

func nearestPubDate(wf []WFItem) (int, float32) {
	var max float32 = 0.00
	var index int = 0

	now := time.Now()
	for i, el := range wf {
		if el.PubDate != "" {
			var value float32 = float32(now.Unix()) / float32(el.PubDateTime.Unix())

			if value > max {
				max = value
				index = i
			}
		}
	}
	return index, max
}

func nearestExpiryDate(wf []WFItem) (int, float32) {
	var max float32 = 0.00
	var index int = 0

	now := time.Now()
	for i, el := range wf {
		if el.ExpiryDate != "" {
			var value float32 = float32(now.Unix()) / float32(el.ExpiryDateTime.Unix())

			if value > max {
				max = value
				index = i
			}
		}
	}
	return index, max
}

func messageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Content == "!alert" {
		s.ChannelMessageSend(m.ChannelID, msg)
	}
}
