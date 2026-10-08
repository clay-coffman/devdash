package work

import (
	"bytes"
	"github.com/clay-coffman/devdash/internal/collect"
	"time"
)

func ReadBoard() (Board, error) { return ReadBoardSocket(SocketContext()) }

func ReadBoardSocket(socket string) (Board, error) {
	b, err := collect.RunBoundedSocket(10*time.Second, MaxDocument, socket, "herdr-board", "json", "--no-sweep")
	if err != nil {
		return Board{}, err
	}
	return ParseBoard(bytes.NewReader(b))
}
