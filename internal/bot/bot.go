package bot

import (
	"errors"
	"math"
	"math/rand/v2"

	"GridKing-Backend/internal/game"
)

type Difficulty string

const (
	Easy   Difficulty = "easy"
	Medium Difficulty = "medium"
	Hard   Difficulty = "hard"
)

func Choose(state game.State, difficulty Difficulty) (game.Move, error) {
	depth := 0
	switch difficulty {
	case Easy:
		depth = 2
	case Medium:
		depth = 4
	case Hard:
		depth = 6
	default:
		return game.Move{}, errors.New("difficulty must be easy, medium, or hard")
	}
	moves := state.LegalMoves()
	if len(moves) == 0 {
		return game.Move{}, errors.New("no legal moves")
	}
	maximizing := state.Turn
	bestScore := math.MinInt
	best := make([]game.Move, 0, len(moves))
	for _, move := range moves {
		next, _ := state.Apply(move)
		score := search(next, depth-1, math.MinInt, math.MaxInt, maximizing, difficulty)
		if score > bestScore {
			bestScore = score
			best = []game.Move{move}
		} else if score == bestScore {
			best = append(best, move)
		}
	}
	return best[rand.IntN(len(best))], nil
}

func search(state game.State, depth, alpha, beta int, maximizing game.Color, difficulty Difficulty) int {
	if depth == 0 || state.Winner != 0 {
		return evaluate(state, maximizing, difficulty)
	}
	moves := state.LegalMoves()
	if state.Turn == maximizing {
		value := math.MinInt
		for _, move := range moves {
			next, _ := state.Apply(move)
			value = max(value, search(next, depth-1, alpha, beta, maximizing, difficulty))
			alpha = max(alpha, value)
			if alpha >= beta {
				break
			}
		}
		return value
	}
	value := math.MaxInt
	for _, move := range moves {
		next, _ := state.Apply(move)
		value = min(value, search(next, depth-1, alpha, beta, maximizing, difficulty))
		beta = min(beta, value)
		if alpha >= beta {
			break
		}
	}
	return value
}

func evaluate(state game.State, perspective game.Color, difficulty Difficulty) int {
	if state.Winner != 0 {
		if state.Winner == perspective {
			return 100000
		}
		return -100000
	}
	score := 0
	for index, piece := range state.Board {
		if piece == game.Empty {
			continue
		}
		row, col := index/8, index%8
		value := 100
		if piece == game.RedKing || piece == game.BlackKing {
			value = 175
		}
		if difficulty == Medium || difficulty == Hard {
			if col == 0 || col == 7 {
				value += 12
			}
		}
		if difficulty == Hard {
			if piece == game.RedMan {
				value += (7 - row) * 6
			} else if piece == game.BlackMan {
				value += row * 6
			}
		}
		isPerspective := (perspective == game.Red && (piece == game.RedMan || piece == game.RedKing)) ||
			(perspective == game.Black && (piece == game.BlackMan || piece == game.BlackKing))
		if isPerspective {
			score += value
		} else {
			score -= value
		}
	}
	return score
}
