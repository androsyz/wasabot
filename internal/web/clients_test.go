package web

import (
	"context"
	"strings"
	"testing"
)

func TestClientsPage_ListsClientsWithTheirData(t *testing.T) {
	body := newFixture(t).get("/clients").Body.String()

	// creation order, not alphabetical
	positions := []int{
		strings.Index(body, "Toko Kopi Senja</span>"),
		strings.Index(body, "Klinik Gigi Sehat</span>"),
		strings.Index(body, "Laundry Bersih</span>"),
		strings.Index(body, "Kos Melati</span>"),
	}
	for i := 1; i < len(positions); i++ {
		if positions[i-1] < 0 || positions[i] < positions[i-1] {
			t.Fatalf("clients are not listed in creation order: %v", positions)
		}
	}

	for _, want := range []string{
		"Connected", "Pairing", "Stopped", "Logged out",
		"62 811 xxxx xxxx", "62 812 xxxx xxxx", "62 813 xxxx xxxx", "Not linked", // html/template writes + as &#43;
		"&#39;agent.md&#39; | test-model",
		"Today: 2<br>Month: 3", // 2 today plus 1 earlier this month; last month's messages are not counted
		"Today: 1<br>Month: 1",
		"Today: 0<br>Month: 0",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page does not contain %q", want)
		}
	}
}

func TestClientsPage_NeverShowsAFullPhoneNumber(t *testing.T) {
	body := newFixture(t).get("/clients").Body.String()

	for _, secret := range []string{"6281134567890", "6281234567890", "1134567890", "34567890"} {
		if strings.Contains(body, secret) {
			t.Fatalf("the page leaks %q", secret)
		}
	}
}

func TestClientsPage_StatusBarAndHeader(t *testing.T) {
	body := newFixture(t).get("/clients").Body.String()

	for _, want := range []string{"1 Connected", "2 Stopped", "1 Connecting"} {
		if !strings.Contains(body, want) {
			t.Errorf("status bar does not contain %q", want)
		}
	}
	if !strings.Contains(body, "WhatsApp Connected") {
		t.Error("the header shows the first client's status by default")
	}
	if !strings.Contains(body, `<option value="1" selected>Client: Toko Kopi Senja</option>`) {
		t.Error("the picker selects the first client by default")
	}
	if !strings.Contains(body, `aria-current="page"`) || !strings.Contains(body, "Active page") {
		t.Error("the current page is marked in the navigation")
	}
}

func TestClientsPage_SelectedClientDrivesTheHeader(t *testing.T) {
	body := newFixture(t).get("/clients?client=3").Body.String()

	if !strings.Contains(body, `<option value="3" selected>Client: Laundry Bersih</option>`) {
		t.Error("the requested client is selected")
	}
	if !strings.Contains(body, "WhatsApp Stopped") {
		t.Error("the header shows the selected client's status")
	}
}

func TestClientsPage_ActionsFollowTheStatus(t *testing.T) {
	body := newFixture(t).get("/clients").Body.String()

	rows := strings.Split(body, "<tr>")
	row := func(name string) string {
		for _, r := range rows {
			if strings.Contains(r, name+"</span>") {
				return r
			}
		}
		t.Fatalf("no row for %s", name)
		return ""
	}

	if r := row("Toko Kopi Senja"); !strings.Contains(r, ">Stop<") || !strings.Contains(r, ">Logout<") || strings.Contains(r, "Show QR") || strings.Contains(r, ">Start<") {
		t.Errorf("connected row actions are wrong: %s", r)
	}
	if r := row("Klinik Gigi Sehat"); !strings.Contains(r, "btn-blue") || !strings.Contains(r, "Show QR") {
		t.Errorf("pairing row needs a Show QR button: %s", r)
	}
	if r := row("Laundry Bersih"); !strings.Contains(r, ">Start<") || strings.Contains(r, "btn-blue") {
		t.Errorf("stopped row needs a Start button: %s", r)
	}
	if r := row("Kos Melati"); !strings.Contains(r, "Show QR") || strings.Contains(r, ">Stop<") {
		t.Errorf("logged-out row needs Show QR and nothing to stop: %s", r)
	}
}

func TestClientsPage_Filtering(t *testing.T) {
	f := newFixture(t)

	names := []string{"Toko Kopi Senja", "Klinik Gigi Sehat", "Laundry Bersih", "Kos Melati"}
	listed := func(body string) []string {
		var out []string
		for _, n := range names {
			if strings.Contains(body, n+"</span>") {
				out = append(out, n)
			}
		}
		return out
	}

	tests := []struct {
		name, query string
		want        []string
	}{
		{"no filter", "", names},
		{"search is case-insensitive", "?q=KLINIK", []string{"Klinik Gigi Sehat"}},
		{"search matches part of a name", "?q=la", []string{"Laundry Bersih", "Kos Melati"}},
		{"status filter", "?status=stopped", []string{"Laundry Bersih"}},
		{"search and status together", "?q=kos&status=logged_out", []string{"Kos Melati"}},
		{"search and status that exclude each other", "?q=kos&status=connected", nil},
		{"an unknown status falls back to all", "?status=bogus", names},
		{"surrounding spaces are ignored", "?q=%20melati%20", []string{"Kos Melati"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := listed(f.get("/clients" + tt.query).Body.String())

			if strings.Join(got, ",") != strings.Join(tt.want, ",") {
				t.Fatalf("listed %v, want %v", got, tt.want)
			}
		})
	}
}

func TestClientsPage_FilteringDoesNotChangeTheStatusBar(t *testing.T) {
	body := newFixture(t).get("/clients?q=zzz").Body.String()

	if !strings.Contains(body, "1 Connected") || !strings.Contains(body, "2 Stopped") {
		t.Fatal("the status bar counts every client, not just the ones that match the search")
	}
}

func TestClientsPage_HTMXGetsOnlyTheTable(t *testing.T) {
	f := newFixture(t)

	fragment := f.get("/clients?q=klinik", "HX-Request", "true", "HX-Target", "clients-results").Body.String()
	if strings.Contains(fragment, "<html") || strings.Contains(fragment, "Clients Overview") || !strings.HasPrefix(fragment, "<table") {
		t.Fatalf("an htmx table refresh must return just the table:\n%.200s", fragment)
	}
	if !strings.Contains(fragment, "Klinik Gigi Sehat") || strings.Contains(fragment, "Laundry Bersih") {
		t.Fatal("the fragment must apply the filter")
	}

	full := f.get("/clients", "HX-Request", "true", "HX-Target", "something-else").Body.String()
	if !strings.Contains(full, "<html") {
		t.Fatal("an htmx request aimed elsewhere still needs the full page")
	}
}

func TestClientsPage_EmptyStates(t *testing.T) {
	t.Run("no clients at all", func(t *testing.T) {
		rec := newEmptyFixture(t).get("/clients")

		body := rec.Body.String()
		if rec.Code != 200 || !strings.Contains(body, "No clients yet.") || strings.Contains(body, "client-picker") {
			t.Fatalf("status %d, empty dashboard:\n%s", rec.Code, body)
		}
		if !strings.Contains(body, "0 Connected") || !strings.Contains(body, "0 Stopped") {
			t.Fatal("the status bar still renders")
		}
	})

	t.Run("nothing matches", func(t *testing.T) {
		body := newFixture(t).get("/clients?q=zzz").Body.String()

		if !strings.Contains(body, "No clients match your search.") {
			t.Fatal("want the no-match message")
		}
	})
}

func TestClientsPage_EscapesClientNames(t *testing.T) {
	f := newFixture(t)
	if _, err := f.st.Clients.Create(context.Background(), `<script>alert(1)</script>`); err != nil {
		t.Fatal(err)
	}

	body := f.get("/clients").Body.String()

	if strings.Contains(body, "<script>alert(1)") {
		t.Fatal("a client name reached the page unescaped")
	}
	if !strings.Contains(body, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Fatal("the name should appear, escaped")
	}
}

func TestClientsPage_WithoutARuntimeFallsBackToStoredState(t *testing.T) {
	f := newFixture(t, func(o *Options) { o.Runtime = nil })

	body := f.get("/clients").Body.String()

	// nothing can be connected without a runtime: 3 linked clients are stopped, 1 is logged out
	if !strings.Contains(body, "0 Connected") || !strings.Contains(body, "4 Stopped") {
		t.Fatalf("status bar wrong:\n%.400s", body)
	}
	if strings.Count(body, "Logged out</span>") != 1 {
		t.Fatal("only the client without a linked number is logged out")
	}
}

func TestClientsPage_SearchTermIsEscapedInTheInput(t *testing.T) {
	body := newFixture(t).get(`/clients?q="><script>alert(1)</script>`).Body.String()

	if strings.Contains(body, "<script>alert(1)") {
		t.Fatal("the search term reached the page unescaped")
	}
}

func TestMaskPhone(t *testing.T) {
	tests := []struct{ jid, want string }{
		{"6281234567890:7@s.whatsapp.net", "+62 812 xxxx xxxx"},
		{"6281234567890@s.whatsapp.net", "+62 812 xxxx xxxx"},
		{"628123456789@s.whatsapp.net", "+62 812 xxxx xxx"},
		{"14155550123@s.whatsapp.net", "+14 155 xxxx xx"},
		{"", ""},
		{"12345@s.whatsapp.net", ""},
		{"abc@lid", ""},
		{"62812345678x@s.whatsapp.net", ""},
	}
	for _, tt := range tests {
		if got := maskPhone(tt.jid); got != tt.want {
			t.Errorf("maskPhone(%q) = %q, want %q", tt.jid, got, tt.want)
		}
	}
}

func TestClientStatus(t *testing.T) {
	tests := []struct {
		status      ClientStatus
		label, tone string
	}{
		{StatusConnected, "Connected", "ok"},
		{StatusPairing, "Pairing", "wait"},
		{StatusReconnecting, "Reconnecting", "wait"},
		{StatusStopped, "Stopped", "bad"},
		{StatusLoggedOut, "Logged out", "off"},
	}
	for _, tt := range tests {
		if tt.status.Label() != tt.label || tt.status.Tone() != tt.tone {
			t.Errorf("%s: label %q tone %q, want %q %q", tt.status, tt.status.Label(), tt.status.Tone(), tt.label, tt.tone)
		}
	}
}
