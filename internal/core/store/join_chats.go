package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// The extra required chats (migration 00029, docs/03-bot-ux.md): the channels
// and groups customers must join besides the first one, which is the settings
// join.channel and join.link.

// JoinChat is one extra required chat. Position counts from 1 (the first extra,
// right after the setting's chat).
type JoinChat struct {
	Position int
	Chat     string
	Link     string
	Label    string
}

// joinChatsLockKey is the advisory lock of the join chats list.
const joinChatsLockKey int64 = 0x6a6f696e // "join"

// ListJoinChats lists the extra required chats in the order customers are asked.
func (s *Store) ListJoinChats(ctx context.Context, q querier) ([]JoinChat, error) {
	rows, err := q.Query(ctx, `SELECT position, chat, link, label FROM core.join_chats ORDER BY position`)
	if err != nil {
		return nil, fmt.Errorf("list join chats: %w", err)
	}
	defer rows.Close()
	var out []JoinChat
	for rows.Next() {
		var c JoinChat
		if err := rows.Scan(&c.Position, &c.Chat, &c.Link, &c.Label); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// LockJoinChats holds the list until the transaction ends, so that a change and
// the audit entries written from what it read agree, and two changes take turns.
func (s *Store) LockJoinChats(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, joinChatsLockKey); err != nil {
		return fmt.Errorf("lock join chats: %w", err)
	}
	return nil
}

// ReplaceJoinChats writes the whole list: the chats are given in order and take
// the positions 1, 2, 3... whatever Position they carry.
func (s *Store) ReplaceJoinChats(ctx context.Context, tx pgx.Tx, chats []JoinChat) error {
	if _, err := tx.Exec(ctx, `DELETE FROM core.join_chats`); err != nil {
		return fmt.Errorf("clear join chats: %w", err)
	}
	for i, c := range chats {
		if _, err := tx.Exec(ctx, `INSERT INTO core.join_chats (position, chat, link, label) VALUES ($1, $2, $3, $4)`,
			i+1, c.Chat, c.Link, c.Label); err != nil {
			return fmt.Errorf("save join chat: %w", err)
		}
	}
	return nil
}
