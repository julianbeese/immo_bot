// One-off IMAP probe: lists recent INBOX mail (with and without IS24 sender filter).
// Usage: EMAIL_PASSWORD=... go run ./scripts/imap_probe
package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

func main() {
	user := envOr("EMAIL_USERNAME", "julianbeese@web.de")
	pass := os.Getenv("EMAIL_PASSWORD")
	if pass == "" {
		fmt.Fprintln(os.Stderr, "EMAIL_PASSWORD required")
		os.Exit(1)
	}
	host := envOr("EMAIL_IMAP_HOST", "imap.web.de:993")

	cl, err := imapclient.DialTLS(host, nil)
	if err != nil {
		fmt.Println("DIAL ERROR:", err)
		os.Exit(1)
	}
	defer cl.Close()

	if err := cl.Login(user, pass).Wait(); err != nil {
		fmt.Println("LOGIN ERROR:", err)
		os.Exit(1)
	}
	defer cl.Logout()

	sel, err := cl.Select("INBOX", &imap.SelectOptions{ReadOnly: true}).Wait()
	if err != nil {
		fmt.Println("SELECT ERROR:", err)
		os.Exit(1)
	}
	fmt.Printf("INBOX: %d messages total\n", sel.NumMessages)

	since := time.Now().Add(-30 * 24 * time.Hour)
	searchData, err := cl.UIDSearch(&imap.SearchCriteria{Since: since}, nil).Wait()
	if err != nil {
		fmt.Println("SEARCH ERROR:", err)
		os.Exit(1)
	}
	uids := searchData.AllUIDs()
	fmt.Printf("Last 30 days: %d messages (IMAP SINCE)\n\n", len(uids))
	if len(uids) == 0 {
		return
	}

	// Fetch last 20 by UID
	start := 0
	if len(uids) > 20 {
		start = len(uids) - 20
	}
	subset := uids[start:]
	fetchOpts := &imap.FetchOptions{Envelope: true, UID: true}
	buffers, err := cl.Fetch(imap.UIDSetNum(subset...), fetchOpts).Collect()
	if err != nil {
		fmt.Println("FETCH ERROR:", err)
		os.Exit(1)
	}

	is24 := 0
	for _, b := range buffers {
		if b.Envelope == nil {
			continue
		}
		from := formatAddr(b.Envelope.From)
		subj := b.Envelope.Subject
		match := strings.Contains(strings.ToLower(from), "immobilienscout24") ||
			strings.Contains(strings.ToLower(from), "immoscout24")
		if match {
			is24++
		}
		tag := " "
		if match {
			tag = "*"
		}
		fmt.Printf("%s UID=%d  %s  %q\n", tag, b.UID, b.Envelope.Date.Format("2006-01-02"), from)
		fmt.Printf("    Betreff: %s\n", subj)
	}
	fmt.Printf("\n* = IS24 sender match (%d of %d shown)\n", is24, len(buffers))
}

func envOr(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

func formatAddr(addrs []imap.Address) string {
	if len(addrs) == 0 {
		return ""
	}
	a := addrs[0]
	mail := a.Mailbox + "@" + a.Host
	if a.Name != "" {
		return a.Name + " <" + mail + ">"
	}
	return mail
}
