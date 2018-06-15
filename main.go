package main

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"github.com/bwmarrin/discordgo"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	"golang.org/x/net/html/charset"
	"io/ioutil"
	"net/http"
	"regexp"
	"runtime"
	"strings"
	"time"
)

const wfurl = "http://content.warframe.com/dynamic/rss.php"

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

var token, webhookID, webhookToken string

var msg string
var wfrss *WFRSS
var data chan *WFRSS
var pattern *regexp.Regexp
var ignoreExp map[string]bool = make(map[string]bool, 1)
var ignorePub map[string]bool = make(map[string]bool, 1)
var ignorePubAnt map[string]bool = make(map[string]bool, 1)

func main() {
	runtime.GOMAXPROCS(runtime.NumCPU())

	pattern = regexp.MustCompile(`[\/: ]`)
	ignoreExp = make(map[string]bool, 1)
	ignorePub = make(map[string]bool, 1)
	ignorePubAnt = make(map[string]bool, 1)

	done := make(chan bool)
	data = make(chan *WFRSS)
	wfrss = new(WFRSS)

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

func init() {
	viper.SetConfigFile("./credentials.json")

	if err := viper.ReadInConfig(); err != nil {
		log.Fatalf("Error reading config file, %s", err)
	}

	token = viper.GetString("botToken")
	webhookID = viper.GetString("webhookID")
	webhookToken = viper.GetString("webhookToken")
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

	/*log.Infoln(rateExp, wfi.ExpiryDateTime.String())
	log.Infoln("Processing rss")*/
	if ignoreExp[wfi.Guid] == false {
		if rateExp == 1.00 {
			msg = fmt.Sprintf("**EXPIRADO!!!!**\n%s", alertMessage(now, &wfi))
			dg.WebhookExecute(webhookID, webhookToken, false, &discordgo.WebhookParams{
				Content: msg,
			})
			ignoreExp[wfi.Guid] = true
		}
	}

	wfi = wf.Item[pubIndex]
	if ignorePubAnt[wfi.Guid] == false {
		minute := 10
		tmpMsg := fmt.Sprintf("**ALERTA!!!!!**\n%s\n", alertMessage(now, &wfi))
		if now.Unix() == wfi.PubDateTime.Add(time.Duration(-minute)*time.Minute).Unix() {
			dg.WebhookExecute(webhookID, webhookToken, false, &discordgo.WebhookParams{
				Content: tmpMsg,
			})
		}
		ignorePubAnt[wfi.Guid] = true
	}

	msg = alertMessage(now, &wfi)
	if ignorePub[wfi.Guid] == false {
		if ratePub == 1.00 {
			log.Info("Sending content to webhook")
			dg.WebhookExecute(webhookID, webhookToken, false, &discordgo.WebhookParams{
				Content: msg,
			})
			ignorePub[wfi.Guid] = true
		}
	}
	wfrss = wf
	data <- wf
}

func notification(dg *discordgo.Session, data chan *WFRSS) {
	c := time.Tick(200 * time.Millisecond)
	for now := range c {
		now.Second()
		//log.Info("Processing notification at: ", now)
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
		nowUnix := now.Unix()
		pubUnix := el.PubDateTime.Unix()
		if el.PubDate != "" && nowUnix >= pubUnix {
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
		nowUnix := now.Unix()
		expiryUnix := el.ExpiryDateTime.Unix()
		if el.ExpiryDate != "" && nowUnix >= expiryUnix {
			var value float32 = float32(nowUnix) / float32(expiryUnix)

			if value > max {
				max = value
				index = i
			}
		}
	}
	return index, max
}

func messageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	c := m.Content
	if strings.HasPrefix(c, "wf!") {
		if strings.HasSuffix(c, "alert") {
			s.ChannelMessageSend(m.ChannelID, msg)
		}

		if strings.HasSuffix(c, "alerts") {
			var tmpMsg string
			now := time.Now()
			for _, el := range wfrss.Item {
				if el.PubDate != "" && el.ExpiryDate != "" {
					tmpMsg += alertMessage(now, &el)
				}
			}
			s.ChannelMessageSend(m.ChannelID, tmpMsg)
		}
	}
}

func alertMessage(t time.Time, item *WFItem) string {
	lp := item.PubDateTime.Local()
	le := item.ExpiryDateTime.Local()

	start := "JA COMECOU CARA!!!"
	expiry := "PERDEU PLAYBOY!"
	subPub := item.PubDateTime.Sub(t)
	subExp := item.ExpiryDateTime.Sub(t)

	if m := subPub.Minutes(); m >= 0.00 {
		start = fmt.Sprintf("***%dm***", int(m))
	}

	if m := subExp.Minutes(); m >= 0.00 {
		expiry = fmt.Sprintf("***%dm***", int(m))
	}

	starts := fmt.Sprintf("%d/%d/%d %d:%d:%d", lp.Day(), lp.Month(), lp.Year(), lp.Hour(), lp.Minute(), lp.Second())
	ends := fmt.Sprintf("%d/%d/%d %d:%d:%d", le.Day(), le.Month(), le.Year(), le.Hour(), le.Minute(), le.Second())
	starts = addZerosToDateHours(pattern, starts)
	ends = addZerosToDateHours(pattern, ends)

	ret := fmt.Sprintf("**Titulo:** %s\n**Inicia em:** %s *(%s)*\n**Expira em:** %s *(%s)*\n**Tipo:** %s\n\n", item.Title, start, starts, expiry, ends, item.Author)

	return ret
}

func addZerosToDateHours(r *regexp.Regexp, s string) string {
	array := r.Split(s, -1)

	for i := range array {
		if len(array[i]) == 1 {
			array[i] = "0" + array[i]
		}
	}
	return fmt.Sprintf("%s/%s/%s %s:%s:%s", array[0], array[1], array[2], array[3], array[4], array[5])
}
