package statuspage

// texts is every fixed word the public page prints, in one language. The
// page's own words (title, description, public names) come from the
// operator; everything else a visitor reads is a field here, so a language
// is complete exactly when every field is filled in. texts_test.go fails on
// an empty field and on a format string whose verbs differ from English.
//
// Format strings take their values in the English order. A language whose
// word order differs reorders the words around the verbs, not the verbs.
type texts struct {
	// Lang is the document's lang attribute.
	Lang string

	// The status words beside a lamp (design §1.3).
	Up, Degraded, Down, NoData, NotMonitored string

	// The summary sentence (design §2: a count, never an adjective).
	NoServices string // the page lists nothing
	OneUp      string // the page's one service is up
	AllUp      string // all %d services are up
	// ServiceOne and ServiceMany are the noun after "%d of %d".
	ServiceOne, ServiceMany string
	// "%d of %d %s is down": the first count, the total, the noun.
	DownOne, DownMany         string
	DegradedOne, DegradedMany string
	UpOne, UpMany             string
	WithNoData                string // "%d with no data yet"
	NotMonitoredCount         string // "%d not monitored"

	// Updated labels the moment the page was built.
	Updated string
	// Weekdays from Sunday and months from January, abbreviated, for the
	// dates the page prints ("Mon 2 Jan").
	Weekdays [7]string
	Months   [12]string
	// Relative days, lowercase; the page capitalises them where a line
	// starts with one.
	Today, Tomorrow, Yesterday string

	// Durations: "under 1 min", and the units of "2 d 3 h" and "4 h 51 min".
	UnderAMinute               string
	DayUnit, HourUnit, MinUnit string

	// Maintenance.
	Maintenance string // the card's heading and a service's chip
	Scheduled   string
	InProgress  string
	NowUntil    string // "Now, until %s"
	Affects     string // "Affects %s"

	// Services.
	ServicesHeading     string // "Services (%d)"
	CertificateExpiring string
	DaysAgo             string // "%d days ago"
	DayAgo              string // "1 day ago"
	TodayAxis           string // the right end of the history's axis
	// DecimalSeparator writes an uptime percentage.
	DecimalSeparator string
	Uptime           string // "%s uptime, %d days", the first being "99.95%"
	NoUptimeOlder    string // "No uptime data, %d days"
	NoUptimeYet      string
	History          string // "Last %d days: %d up, %d degraded, %d down, %d no data."
	HistoryDown      string // " Down %s."
	HistoryDegraded  string // " Degraded %s."

	// Outages.
	PastOutages string // "Past %d days"
	NoOutages   string // "No outages in the past %d days."
	Recovered   string
	DownSince   string // "Down since %s · %s so far"
	DownFor     string // "Down %s · %s"

	// Footer.
	TimesIn string // "Times in %s"
	// Attribution is the "Monitored with SubGlance" line an operator may
	// hide (Page.CreditShown).
	Attribution string
}

var english = texts{
	Lang: "en",

	Up: "Up", Degraded: "Degraded", Down: "Down", NoData: "No data yet", NotMonitored: "Not monitored",

	NoServices:  "No services on this page yet.",
	OneUp:       "The service is up",
	AllUp:       "All %d services are up",
	ServiceOne:  "service",
	ServiceMany: "services",
	DownOne:     "%d of %d %s is down", DownMany: "%d of %d %s are down",
	DegradedOne: "%d of %d %s is degraded", DegradedMany: "%d of %d %s are degraded",
	UpOne: "%d of %d %s is up", UpMany: "%d of %d %s are up",
	WithNoData:        "%d with no data yet",
	NotMonitoredCount: "%d not monitored",

	Updated:  "Updated",
	Weekdays: [7]string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"},
	Months:   [12]string{"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"},
	Today:    "today", Tomorrow: "tomorrow", Yesterday: "yesterday",

	UnderAMinute: "under 1 min",
	DayUnit:      "d", HourUnit: "h", MinUnit: "min",

	Maintenance: "Maintenance",
	Scheduled:   "Scheduled",
	InProgress:  "In progress",
	NowUntil:    "Now, until %s",
	Affects:     "Affects %s",

	ServicesHeading:     "Services (%d)",
	CertificateExpiring: "Certificate expires soon",
	DaysAgo:             "%d days ago",
	DayAgo:              "1 day ago",
	TodayAxis:           "Today",
	DecimalSeparator:    ".",
	Uptime:              "%s uptime, %d days",
	NoUptimeOlder:       "No uptime data, %d days",
	NoUptimeYet:         "No uptime data yet",
	History:             "Last %d days: %d up, %d degraded, %d down, %d no data.",
	HistoryDown:         " Down %s.",
	HistoryDegraded:     " Degraded %s.",

	PastOutages: "Past %d days",
	NoOutages:   "No outages in the past %d days.",
	Recovered:   "Recovered",
	DownSince:   "Down since %s · %s so far",
	DownFor:     "Down %s · %s",

	TimesIn:     "Times in %s",
	Attribution: "Monitored with SubGlance",
}

// dutch follows the conventions of Dutch status pages: "storing" for an
// outage, "operationeel" for a service that works, a decimal comma, and
// lowercase day and month abbreviations.
var dutch = texts{
	Lang: "nl",

	Up: "Operationeel", Degraded: "Verstoord", Down: "Storing", NoData: "Nog geen gegevens", NotMonitored: "Niet bewaakt",

	NoServices:  "Nog geen diensten op deze pagina.",
	OneUp:       "De dienst is operationeel",
	AllUp:       "Alle %d diensten zijn operationeel",
	ServiceOne:  "dienst",
	ServiceMany: "diensten",
	DownOne:     "%d van %d %s heeft een storing", DownMany: "%d van %d %s hebben een storing",
	DegradedOne: "%d van %d %s is verstoord", DegradedMany: "%d van %d %s zijn verstoord",
	UpOne: "%d van %d %s is operationeel", UpMany: "%d van %d %s zijn operationeel",
	WithNoData:        "%d nog zonder gegevens",
	NotMonitoredCount: "%d niet bewaakt",

	Updated:  "Bijgewerkt",
	Weekdays: [7]string{"zo", "ma", "di", "wo", "do", "vr", "za"},
	Months:   [12]string{"jan", "feb", "mrt", "apr", "mei", "jun", "jul", "aug", "sep", "okt", "nov", "dec"},
	Today:    "vandaag", Tomorrow: "morgen", Yesterday: "gisteren",

	UnderAMinute: "minder dan 1 min",
	DayUnit:      "d", HourUnit: "u", MinUnit: "min",

	Maintenance: "Onderhoud",
	Scheduled:   "Gepland",
	InProgress:  "Bezig",
	NowUntil:    "Nu, tot %s",
	Affects:     "Betreft %s",

	ServicesHeading:     "Diensten (%d)",
	CertificateExpiring: "Certificaat verloopt binnenkort", //nolint:misspell // Dutch, not a misspelled "Certificate"
	DaysAgo:             "%d dagen geleden",
	DayAgo:              "1 dag geleden",
	TodayAxis:           "Vandaag",
	DecimalSeparator:    ",",
	Uptime:              "%s beschikbaar, %d dagen",
	NoUptimeOlder:       "Geen beschikbaarheid bekend, %d dagen",
	NoUptimeYet:         "Nog geen beschikbaarheid bekend",
	History:             "Afgelopen %d dagen: %d operationeel, %d verstoord, %d storing, %d zonder gegevens.",
	HistoryDown:         " Storing %s.",
	HistoryDegraded:     " Verstoord %s.",

	PastOutages: "Afgelopen %d dagen",
	NoOutages:   "Geen storingen in de afgelopen %d dagen.",
	Recovered:   "Hersteld",
	DownSince:   "Storing sinds %s · %s tot nu toe",
	DownFor:     "Storing van %s · %s",

	TimesIn:     "Tijden in %s",
	Attribution: "Bewaakt met SubGlance",
}

// languages holds every table by its store.StatusPageLanguages code.
var languages = map[string]*texts{"en": &english, "nl": &dutch}

// textsFor returns the table for a language code, English for an unknown
// or empty one: a page always renders, and English is what it said before
// it had a language.
func textsFor(lang string) *texts {
	if t, ok := languages[lang]; ok {
		return t
	}
	return &english
}
