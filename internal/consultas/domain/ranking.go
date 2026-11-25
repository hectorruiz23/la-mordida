package domain

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/hectorruiz23/la-mordida/internal/kernel"
)

// ErrUnknownBuyer says the requested buyer identifier is not in the stored
// identities. It is not an empty ranking: no data was ever imported for it.
var ErrUnknownBuyer = errors.New("unknown buyer")

// ErrInvalidEntityKey says the key does not name an entity of the ranking.
var ErrInvalidEntityKey = errors.New("invalid entity key")

// RankingQuery is the scope every part of the reading shares: the buyer, the
// award dates and the page. From and To are civil dates (YYYY-MM-DD) and are
// inclusive; an empty bound leaves that side open.
type RankingQuery struct {
	BuyerScheme string
	BuyerID     string
	From        string
	To          string
	Limit       int
	Offset      int
}

// UnitKey is the stable key of the counting unit: one current award per
// procedure and lot. A global award has no lot, and its key still exists with
// the empty lot.
func UnitKey(expedienteID int64, lotID string) string {
	return fmt.Sprintf("%d:%s", expedienteID, lotID)
}

// PartyKey names a winner by its accredited identifier. A name never becomes
// the key, so two homonyms stay two entities.
func PartyKey(scheme, id string) string {
	return "party:" + scheme + "|" + id
}

// EntityKey names an entity the ranking knows by an internal id.
func EntityKey(id int64) string {
	return "entity:" + strconv.FormatInt(id, 10)
}

// UnitEntityKey names the unidentified winner of one unit. It is one entity
// per award, because a missing identifier is unknown and merging by name
// would invent a group of homonyms.
func UnitEntityKey(unitKey string) string {
	return "unit:" + unitKey
}

// ParseEntityKey splits a key produced by the ranking. It accepts an internal
// entity, an accredited party or a single unit's unidentified winner.
func ParseEntityKey(key string) (kind string, entityID int64, scheme, id, unitKey string, err error) {
	switch {
	case strings.HasPrefix(key, "entity:"):
		entityID, err = strconv.ParseInt(strings.TrimPrefix(key, "entity:"), 10, 64)
		if err != nil || entityID <= 0 {
			return "", 0, "", "", "", ErrInvalidEntityKey
		}
		return "entity", entityID, "", "", "", nil
	case strings.HasPrefix(key, "party:"):
		rest := strings.TrimPrefix(key, "party:")
		scheme, id, ok := strings.Cut(rest, "|")
		if !ok || scheme == "" || id == "" {
			return "", 0, "", "", "", ErrInvalidEntityKey
		}
		return "party", 0, scheme, id, "", nil
	case strings.HasPrefix(key, "unit:"):
		unitKey = strings.TrimPrefix(key, "unit:")
		if unitKey == "" {
			return "", 0, "", "", "", ErrInvalidEntityKey
		}
		return "unit", 0, "", "", unitKey, nil
	default:
		return "", 0, "", "", "", ErrInvalidEntityKey
	}
}

// EntityRef identifies a party in the ranking with every name and identifier
// documented for it. The key is what the detail reading accepts.
type EntityRef struct {
	Key        string   `json:"key"`
	Kind       string   `json:"kind,omitempty"`
	Scheme     string   `json:"scheme,omitempty"`
	ID         string   `json:"id,omitempty"`
	Name       string   `json:"name,omitempty"`
	Names      []string `json:"names,omitempty"`
	Identities []Party  `json:"identities,omitempty"`
}

// Membership is a documented member of a group or a UTE, with the source and
// the validity it was published with. Percentage is the published percentage
// in decimal, empty when the source does not publish one. It never splits an
// amount the source publishes for the group as a whole.
type Membership struct {
	Member     EntityRef `json:"member"`
	Source     string    `json:"source"`
	ValidFrom  string    `json:"valid_from,omitempty"`
	ValidTo    string    `json:"valid_to,omitempty"`
	Percentage string    `json:"percentage,omitempty"`
}

// GroupLink is the dated link between an entity and the group or legal holder
// it belongs to.
type GroupLink struct {
	Group     EntityRef `json:"group"`
	Relation  string    `json:"relation"`
	Source    string    `json:"source"`
	ValidFrom string    `json:"valid_from,omitempty"`
	ValidTo   string    `json:"valid_to,omitempty"`
}

// Entity is a ranked party with its documented relations. Members are kept
// beside the win count, never joined into it: a UTE counts once for the UTE,
// not once per member.
type Entity struct {
	EntityRef
	Members []Membership `json:"members,omitempty"`
	Groups  []GroupLink  `json:"groups,omitempty"`
}

// WinnerRank is one row of the ranking. Wins is the numerator and Percentage
// is the exact decimal share of Total, rounded to two places only here.
type WinnerRank struct {
	Entity     Entity `json:"entity"`
	Wins       int    `json:"wins"`
	Percentage string `json:"percentage"`
}

// Ranking is the reading of who wins the most awards inside one scope. Total
// is the denominator: every counted award unit under the same filters as the
// rows, not the sum of the page.
type Ranking struct {
	Buyer   *Party       `json:"buyer,omitempty"`
	From    string       `json:"from,omitempty"`
	To      string       `json:"to,omitempty"`
	Total   int          `json:"total"`
	Offset  int          `json:"offset"`
	Limit   int          `json:"limit"`
	Ranking []WinnerRank `json:"ranking"`
}

// AwardLine is one counted award with the publication that sustains it and
// the documents it announced. A member of a UTE never receives a share of
// these amounts.
type AwardLine struct {
	UnitKey     string         `json:"unit_key"`
	FolderID    string         `json:"folder_id"`
	Object      string         `json:"object,omitempty"`
	Buyer       Party          `json:"buyer"`
	LotID       string         `json:"lot_id,omitempty"`
	LotObject   string         `json:"lot_object,omitempty"`
	AwardDate   string         `json:"award_date,omitempty"`
	ResultCode  string         `json:"result_code"`
	Winner      Party          `json:"winner"`
	State       string         `json:"state"`
	Amounts     []kernel.Money `json:"amounts,omitempty"`
	Contract    *Contract      `json:"contract,omitempty"`
	Publication Publication    `json:"publication"`
	Documents   []Document     `json:"documents,omitempty"`
}

// AwardPage is the justifying list of one ranked entity. Total counts every
// award of the entity in the scope, not the page.
type AwardPage struct {
	Entity Entity      `json:"entity"`
	Total  int         `json:"total"`
	Offset int         `json:"offset"`
	Limit  int         `json:"limit"`
	Awards []AwardLine `json:"awards"`
}

// Percentage renders wins out of total as a decimal share with two places.
// The arithmetic is integer: no float64 touches a published number.
func Percentage(wins, total int) string {
	if total <= 0 || wins < 0 {
		return "0.00"
	}
	basisPoints := (int64(wins)*10000*2 + int64(total)) / (2 * int64(total))
	return fmt.Sprintf("%d.%02d", basisPoints/100, basisPoints%100)
}
