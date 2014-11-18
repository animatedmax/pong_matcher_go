package main

import (
	"database/sql"
	"fmt"
	"github.com/coopernurse/gorp"
	_ "github.com/go-sql-driver/mysql"
	"github.com/nu7hatch/gouuid"
	"github.com/rubenv/sql-migrate"
	"gopkg.in/guregu/null.v2"
	"net/url"
	"os"
)

func deleteAll() error {
	return dbmap.TruncateTables()
}

func getMatchRequest(uuid string) (bool, MatchRequest, error) {
	matchRequest := MatchRequest{}
	if err := dbmap.SelectOne(
		&matchRequest,
		"SELECT * FROM match_requests WHERE uuid = ?", uuid,
	); err != nil {
		return false, matchRequest, err
	}

	matchId, err := dbmap.SelectStr(
		`SELECT match_id
		FROM participants
		WHERE match_request_uuid = ?
		AND match_id NOT IN (SELECT match_id FROM results)`,
		uuid,
	)

	if err != nil {
		return false, matchRequest, err
	}

	if matchId != "" {
		matchRequest.MatchId = null.StringFrom(matchId)
	}

	return true, matchRequest, nil
}

func getMatch(uuid string) (bool, Match) {
	var participants []Participant
	_, err := dbmap.Select(
		&participants,
		`SELECT * FROM participants WHERE match_id = ?`,
		uuid,
	)
	checkErr(err, "Error getting participants")

	return true, Match{
		Id:              uuid,
		MatchRequest1Id: participants[0].MatchRequestUuid,
		MatchRequest2Id: participants[1].MatchRequestUuid,
	}
}

func persistResult(result Result) error {
	winningParticipantId, err := dbmap.SelectInt(
		`SELECT id
		FROM participants
		WHERE match_id = :match_id
		AND player_id = :player_id`,
		map[string]string{
			"match_id":  result.MatchId,
			"player_id": result.Winner,
		},
	)
	if err != nil {
		return err
	}
	result.WinningParticipantId = winningParticipantId

	losingParticipantId, err := dbmap.SelectInt(
		`SELECT id
		FROM participants
		WHERE match_id = :match_id
		AND player_id = :player_id`,
		map[string]string{
			"match_id":  result.MatchId,
			"player_id": result.Loser,
		},
	)
	if err != nil {
		return err
	}
	result.LosingParticipantId = losingParticipantId

	return dbmap.Insert(&result)
}

func persistMatchRequest(matchRequest MatchRequest) error {
	err := dbmap.Insert(&matchRequest)
	if err != nil {
		return err
	}

	openMatchRequests, err := suitableOpponentMatchRequests(dbmap, matchRequest.RequesterId)
	if len(openMatchRequests) > 0 {
		return recordMatch(dbmap, openMatchRequests[0], matchRequest)
	}
	return err
}

func suitableOpponentMatchRequests(dbmap *gorp.DbMap, requesterId string) ([]MatchRequest, error) {
	var matchRequests []MatchRequest
	_, err := dbmap.Select(
		&matchRequests,
		`SELECT *
		FROM match_requests
		WHERE requester_id <> :requester_id
		AND uuid NOT IN (
			SELECT match_request_uuid
			FROM participants 
		)
		AND requester_id NOT IN (
			SELECT opponent_id
			FROM participants
			WHERE player_id = :requester_id
		)
		LIMIT 1`,
		map[string]string{"requester_id": requesterId},
	)
	return matchRequests, err
}

func recordMatch(dbmap *gorp.DbMap, openMatchRequest MatchRequest, newMatchRequest MatchRequest) error {
	matchIdUuid, err := uuid.NewV4()
	if err != nil {
		return err
	}
	matchId := fmt.Sprintf("%v", matchIdUuid)

	participant1 := Participant{
		MatchId:          matchId,
		MatchRequestUuid: openMatchRequest.Uuid,
		PlayerId:         openMatchRequest.RequesterId,
		OpponentId:       newMatchRequest.RequesterId,
	}
	participant2 := Participant{
		MatchId:          matchId,
		MatchRequestUuid: newMatchRequest.Uuid,
		PlayerId:         newMatchRequest.RequesterId,
		OpponentId:       openMatchRequest.RequesterId,
	}
	return dbmap.Insert(&participant1, &participant2)
}

func formattedUrl(url *url.URL) string {
	return fmt.Sprintf(
		"%v@tcp(%v)%v?parseTime=true",
		url.User,
		url.Host,
		url.Path,
	)
}

func initDb() *gorp.DbMap {
	databaseUrl := os.Getenv("DATABASE_URL")
	if databaseUrl == "" {
		databaseUrl = "mysql2://gopong:gopong@127.0.0.1:3306/pong_matcher_go_development?reconnect=true"
	}

	url, err := url.Parse(databaseUrl)
	checkErr(err, "Error parsing DATABASE_URL")

	db, err := sql.Open("mysql", formattedUrl(url))
	checkErr(err, "failed to establish database connection")

	dbmap := &gorp.DbMap{Db: db, Dialect: gorp.MySQLDialect{"InnoDB", "UTF8"}}

	migrations := &migrate.FileMigrationSource{
		Dir: "db/migrations",
	}
	n, err := migrate.Exec(db, "mysql", migrations, migrate.Up)

	if n > 0 {
		fmt.Printf("Successfully ran %v migrations\n", n)
	}
	checkErr(
		err,
		"Couldn't migrate the database!",
	)

	dbmap.AddTableWithName(MatchRequest{}, "match_requests").SetKeys(true, "Id")
	dbmap.AddTableWithName(Participant{}, "participants").
		SetKeys(true, "Id").
		ColMap("match_request_uuid").SetUnique(true)
	dbmap.AddTableWithName(Result{}, "results").SetKeys(true, "Id")

	return dbmap
}
