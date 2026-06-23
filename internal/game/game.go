package game

import (
	"errors"
	"strings"
)

const (
	Empty = iota
	RedMan
	BlackMan
	RedKing
	BlackKing
)

type Color int

const (
	Red   Color = 1
	Black Color = 2
)

type Move struct {
	Path []int `json:"path"`
}

type MoveAnalysis struct {
	Ply            int    `firestore:"ply" json:"ply"`
	PlayedMove     Move   `firestore:"played_move" json:"played_move"`
	BestMove       Move   `firestore:"best_move" json:"best_move"`
	ScoreBefore    int    `firestore:"score_before" json:"score_before"`
	ScoreAfter     int    `firestore:"score_after" json:"score_after"`
	ScoreLoss      int    `firestore:"score_loss" json:"score_loss"`
	Classification string `firestore:"classification" json:"classification"`
}

type State struct {
	Board  [64]int `json:"board"`
	Turn   Color   `json:"turn"`
	Winner Color   `json:"winner,omitempty"`
	Reason string  `json:"reason,omitempty"`
}

func NewState() State {
	state := State{Turn: Red}
	for row := 0; row < 3; row++ {
		for col := 0; col < 8; col++ {
			if (row+col)%2 == 1 {
				state.Board[row*8+col] = BlackMan
			}
		}
	}
	for row := 5; row < 8; row++ {
		for col := 0; col < 8; col++ {
			if (row+col)%2 == 1 {
				state.Board[row*8+col] = RedMan
			}
		}
	}
	return state
}

func (s State) LegalMoves() []Move {
	if s.Winner != 0 {
		return nil
	}
	jumps := make([]Move, 0)
	for i, piece := range s.Board {
		if colorOf(piece) == s.Turn {
			collectJumps(s.Board, i, piece, []int{i}, &jumps)
		}
	}
	if len(jumps) > 0 {
		return jumps
	}
	moves := make([]Move, 0)
	for from, piece := range s.Board {
		if colorOf(piece) != s.Turn {
			continue
		}
		row, col := from/8, from%8
		for _, direction := range directions(piece) {
			toRow, toCol := row+direction[0], col+direction[1]
			if inside(toRow, toCol) && s.Board[toRow*8+toCol] == Empty {
				moves = append(moves, Move{Path: []int{from, toRow*8 + toCol}})
			}
		}
	}
	return moves
}

func (s State) Apply(move Move) (State, error) {
	if len(move.Path) < 2 {
		return s, errors.New("a move requires at least two squares")
	}
	var selected *Move
	for _, legal := range s.LegalMoves() {
		if samePath(legal.Path, move.Path) {
			copy := legal
			selected = &copy
			break
		}
	}
	if selected == nil {
		return s, errors.New("illegal move; captures are mandatory and jump sequences must be completed")
	}
	next := s
	piece := next.Board[selected.Path[0]]
	next.Board[selected.Path[0]] = Empty
	for i := 1; i < len(selected.Path); i++ {
		from, to := selected.Path[i-1], selected.Path[i]
		if abs(from/8-to/8) == 2 {
			mid := ((from/8+to/8)/2)*8 + (from%8+to%8)/2
			next.Board[mid] = Empty
		}
	}
	last := selected.Path[len(selected.Path)-1]
	if piece == RedMan && last/8 == 0 {
		piece = RedKing
	}
	if piece == BlackMan && last/8 == 7 {
		piece = BlackKing
	}
	next.Board[last] = piece
	next.Turn = opponent(s.Turn)
	if len(next.LegalMoves()) == 0 {
		next.Winner = s.Turn
		next.Reason = "no_moves"
	}
	return next, nil
}

func (s State) CapturedPieces(move Move) []int {
	captured := make([]int, 0, len(move.Path)-1)
	for i := 1; i < len(move.Path); i++ {
		from, to := move.Path[i-1], move.Path[i]
		if abs(from/8-to/8) != 2 {
			continue
		}
		middle := ((from/8+to/8)/2)*8 + (from%8+to%8)/2
		if s.Board[middle] != Empty {
			captured = append(captured, s.Board[middle])
		}
	}
	return captured
}

func (s State) WithWinner(winner Color, reason string) State {
	s.Winner = winner
	s.Reason = reason
	return s
}

func ParseColor(value string) (Color, error) {
	switch strings.ToLower(value) {
	case "red":
		return Red, nil
	case "black":
		return Black, nil
	default:
		return 0, errors.New("color must be red or black")
	}
}

func collectJumps(board [64]int, from, piece int, path []int, result *[]Move) {
	row, col := from/8, from%8
	found := false
	for _, direction := range directions(piece) {
		middleRow, middleCol := row+direction[0], col+direction[1]
		toRow, toCol := row+2*direction[0], col+2*direction[1]
		if !inside(toRow, toCol) || !inside(middleRow, middleCol) {
			continue
		}
		middle, to := middleRow*8+middleCol, toRow*8+toCol
		if board[to] != Empty || colorOf(board[middle]) != opponent(colorOf(piece)) {
			continue
		}
		found = true
		next := board
		next[from], next[middle], next[to] = Empty, Empty, piece
		nextPath := append(append([]int(nil), path...), to)
		promoted := (piece == RedMan && toRow == 0) || (piece == BlackMan && toRow == 7)
		if promoted {
			*result = append(*result, Move{Path: nextPath})
		} else {
			collectJumps(next, to, piece, nextPath, result)
		}
	}
	if !found && len(path) > 1 {
		*result = append(*result, Move{Path: append([]int(nil), path...)})
	}
}

func directions(piece int) [][2]int {
	switch piece {
	case RedMan:
		return [][2]int{{-1, -1}, {-1, 1}}
	case BlackMan:
		return [][2]int{{1, -1}, {1, 1}}
	default:
		return [][2]int{{-1, -1}, {-1, 1}, {1, -1}, {1, 1}}
	}
}

func colorOf(piece int) Color {
	if piece == RedMan || piece == RedKing {
		return Red
	}
	if piece == BlackMan || piece == BlackKing {
		return Black
	}
	return 0
}

func opponent(color Color) Color {
	if color == Red {
		return Black
	}
	return Red
}

func inside(row, col int) bool { return row >= 0 && row < 8 && col >= 0 && col < 8 }
func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
func samePath(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
