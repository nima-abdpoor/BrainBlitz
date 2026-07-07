// Package ws implements the BrainBlitz game-service WebSocket protocol: the
// command envelope sent by the client, the event envelope sent by the
// server, and a Client that manages one connection's lifecycle. It has no
// knowledge of Telegram or of the bot's matchmaking/game business rules —
// see internal/core/matchmaking (and, later, internal/core/game) for that.
package ws

import (
	"encoding/json"
	"fmt"
	"time"
)

// Command is the standard client-to-server envelope documented in
// docs/client/backend-api-analysis.md §4. Only the fields relevant to a
// given Command value need to be populated; the server ignores the rest.
type Command struct {
	Command  string  `json:"command"`
	GameID   string  `json:"gameId,omitempty"`
	MatchID  string  `json:"matchId,omitempty"`
	Category string  `json:"category,omitempty"`
	Players  int     `json:"players,omitempty"`
	Answer   *Answer `json:"answer,omitempty"`
}

// Answer carries an ANSWER command's payload (used starting Phase 5).
type Answer struct {
	GameID     string `json:"gameId"`
	QuestionID string `json:"questionId"`
	Choice     string `json:"choice"`
}

// Command name constants, per backend-api-analysis.md §4.
const (
	CommandAddToWaitingList = "ADD_TO_WAITING_LIST"
	CommandReady            = "READY"
	CommandAnswer           = "ANSWER"
	// CommandGetCategories is a documented no-op server-side
	// (gap-analysis.md G-09) — sending it produces no response. The bot
	// hardcodes categories client-side instead of ever sending this; kept
	// here only for documentation completeness.
	CommandGetCategories = "GET_CATEGORIES"
)

// Event is the standard server-to-client envelope. MetaData's shape depends
// on Event (see the *MetaData decode helpers below) — it is left as raw
// JSON here rather than a concrete struct because different events populate
// it with unrelated shapes (empty, {gameId}, {questions[]}, {leaderBoard}).
type Event struct {
	Success  bool            `json:"success"`
	Event    string          `json:"event"`
	Message  string          `json:"message"`
	MetaData json.RawMessage `json:"metaData"`
}

// Event name constants, per backend-api-analysis.md §4.
const (
	EventAddedToWaitingList = "ADDED_TO_WAITING_LIST"
	EventMatchCreated       = "MATCH_CREATED"
	EventQuestionsPublished = "QUESTIONS_PUBLISHED"
	EventAnswerAccepted     = "ANSWER_ACCEPTED"
	EventCompleted          = "COMPLETED"
	EventError              = "ERROR"
)

// MatchCreatedMetaData is Event.MetaData's shape when Event.Event ==
// EventMatchCreated.
type MatchCreatedMetaData struct {
	GameID string `json:"gameId"`
}

// MatchCreated decodes e.MetaData as MatchCreatedMetaData. Only meaningful
// when e.Event == EventMatchCreated.
func (e Event) MatchCreated() (MatchCreatedMetaData, error) {
	var m MatchCreatedMetaData
	if err := json.Unmarshal(e.MetaData, &m); err != nil {
		return MatchCreatedMetaData{}, fmt.Errorf("decoding %s metadata: %w", EventMatchCreated, err)
	}
	return m, nil
}

// Question is one question in a QUESTIONS_PUBLISHED push, mirroring
// game-service's ProcessGameQuestion (services/game_app/service/param.go).
// There is deliberately no correct-answer field here — the server never
// sends one to clients.
type Question struct {
	ID         string   `json:"id"`
	Content    string   `json:"content"`
	Choices    []string `json:"choices"`
	Difficulty string   `json:"difficulty"`
	// TTL is this question's absolute answer deadline — game-service sends
	// an RFC3339 timestamp (Go's default time.Time JSON encoding), not a
	// duration or relative offset.
	TTL time.Time `json:"ttl"`
}

// QuestionsPublishedMetaData is Event.MetaData's shape when Event.Event ==
// EventQuestionsPublished.
type QuestionsPublishedMetaData struct {
	GameID    string     `json:"gameId"`
	Questions []Question `json:"questions"`
}

// QuestionsPublished decodes e.MetaData as QuestionsPublishedMetaData. Only
// meaningful when e.Event == EventQuestionsPublished.
func (e Event) QuestionsPublished() (QuestionsPublishedMetaData, error) {
	var m QuestionsPublishedMetaData
	if err := json.Unmarshal(e.MetaData, &m); err != nil {
		return QuestionsPublishedMetaData{}, fmt.Errorf("decoding %s metadata: %w", EventQuestionsPublished, err)
	}
	return m, nil
}

// AnswerResult is one already-scored answer within a PlayerPoint entry.
type AnswerResult struct {
	QuestionID    string `json:"questionId"`
	CorrectAnswer string `json:"correctAnswer"`
	PlayerAnswer  string `json:"playerAnswer"`
	IsCorrect     bool   `json:"isCorrect"`
}

// PlayerPoint is one player's standing on the leaderboard. There is no
// username/displayName field — game-service only ever sends the player's
// BrainBlitz user ID (see docs/client/backend-api-analysis.md, leaderboard
// notes).
type PlayerPoint struct {
	PlayerID string         `json:"playerId"`
	Point    int            `json:"point"`
	Answers  []AnswerResult `json:"answers"`
}

// LeaderboardMetaData is the leaderBoard object nested in Event.MetaData
// for both ANSWER_ACCEPTED and COMPLETED — game-service uses the same
// ProcessGameLeaderBoard type for both (services/game_app/service/param.go).
type LeaderboardMetaData struct {
	GameID      string        `json:"gameId"`
	PlayerPoint []PlayerPoint `json:"playerPoint"`
}

// Leaderboard decodes e.MetaData's nested "leaderBoard" object. Only
// meaningful when e.Event == EventAnswerAccepted or e.Event == EventCompleted.
func (e Event) Leaderboard() (LeaderboardMetaData, error) {
	var wrapper struct {
		LeaderBoard LeaderboardMetaData `json:"leaderBoard"`
	}
	if err := json.Unmarshal(e.MetaData, &wrapper); err != nil {
		return LeaderboardMetaData{}, fmt.Errorf("decoding leaderBoard metadata: %w", err)
	}
	return wrapper.LeaderBoard, nil
}
