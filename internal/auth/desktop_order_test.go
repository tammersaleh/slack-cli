package auth

import (
	"strings"
	"testing"
)

func TestSortedTeamIDs(t *testing.T) {
	teams := map[string]desktopTeam{
		"T03": {Name: "c"}, "E01": {Name: "org"}, "T01": {Name: "a"}, "T02": {Name: "b"},
	}
	got := strings.Join(sortedTeamIDs(teams), ",")
	if got != "E01,T01,T02,T03" {
		t.Fatalf("got %s", got)
	}
	if len(sortedTeamIDs(nil)) != 0 {
		t.Fatal("nil map should yield no ids")
	}
}
