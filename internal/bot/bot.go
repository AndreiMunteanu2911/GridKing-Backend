package bot

import (
	"errors"
	"math"
	"math/rand/v2"
	"sort"

	"GridKing-Backend/internal/game"
)

type Difficulty string
type Personality string

const (
	Easy   Difficulty = "easy"
	Medium Difficulty = "medium"
	Hard   Difficulty = "hard"

	Balanced   Personality = "balanced"
	Aggressive Personality = "aggressive"
	Defensive  Personality = "defensive"
	Trickster  Personality = "trickster"
)

type weights struct {
	man, king, edge, advance, center, mobility int
}

type RankedMove struct {
	Move  game.Move `json:"move"`
	Score int       `json:"score"`
}

func ParsePersonality(value Personality) (Personality, error) {
	switch value {
	case "", Balanced:
		return Balanced, nil
	case Aggressive, Defensive, Trickster:
		return value, nil
	default:
		return "", errors.New("personality must be balanced, aggressive, defensive, or trickster")
	}
}

func ValidateDifficulty(value Difficulty) error {
	_, err := depthFor(value)
	return err
}

func Choose(state game.State, difficulty Difficulty, selected ...Personality) (game.Move, error) {
	depth, err := depthFor(difficulty)
	if err != nil {
		return game.Move{}, err
	}
	personality := Balanced
	if len(selected) > 0 {
		personality, err = ParsePersonality(selected[0])
		if err != nil {
			return game.Move{}, err
		}
	}
	ranked := RankMoves(state, depth, personality)
	if len(ranked) == 0 {
		return game.Move{}, errors.New("no legal moves")
	}
	bestCount := 1
	for bestCount < len(ranked) && ranked[bestCount].Score == ranked[0].Score {
		bestCount++
	}
	return ranked[rand.IntN(bestCount)].Move, nil
}

func RankMoves(state game.State, depth int, personality Personality) []RankedMove {
	if depth < 1 {
		depth = 1
	}
	parsed, err := ParsePersonality(personality)
	if err != nil {
		parsed = Balanced
	}
	moves := state.LegalMoves()
	result := make([]RankedMove, 0, len(moves))
	for _, move := range moves {
		next, err := state.Apply(move)
		if err != nil {
			continue
		}
		result = append(result, RankedMove{Move: move, Score: search(next, depth-1, math.MinInt, math.MaxInt, state.Turn, parsed)})
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Score == result[j].Score {
			return pathKey(result[i].Move.Path) < pathKey(result[j].Move.Path)
		}
		return result[i].Score > result[j].Score
	})
	return result
}

func AnalyzeGame(initial game.State, moves []game.Move, depth int) []game.MoveAnalysis {
	state := initial
	result := make([]game.MoveAnalysis, 0, len(moves))
	for ply, played := range moves {
		ranked := RankMoves(state, depth, Balanced)
		if len(ranked) == 0 {
			break
		}
		playedScore := ranked[len(ranked)-1].Score
		for _, candidate := range ranked {
			if samePath(candidate.Move.Path, played.Path) {
				playedScore = candidate.Score
				break
			}
		}
		loss := ranked[0].Score - playedScore
		result = append(result, game.MoveAnalysis{
			Ply:            ply + 1,
			PlayedMove:     played,
			BestMove:       ranked[0].Move,
			ScoreBefore:    ranked[0].Score,
			ScoreAfter:     playedScore,
			ScoreLoss:      loss,
			Classification: classify(loss),
		})
		next, err := state.Apply(played)
		if err != nil {
			break
		}
		state = next
	}
	return result
}

func depthFor(difficulty Difficulty) (int, error) {
	switch difficulty {
	case Easy:
		return 2, nil
	case Medium:
		return 4, nil
	case Hard:
		return 6, nil
	default:
		return 0, errors.New("difficulty must be easy, medium, or hard")
	}
}

func search(state game.State, depth, alpha, beta int, maximizing game.Color, personality Personality) int {
	if depth == 0 || state.Winner != 0 {
		return evaluate(state, maximizing, personality)
	}
	moves := state.LegalMoves()
	if state.Turn == maximizing {
		value := math.MinInt
		for _, move := range moves {
			next, _ := state.Apply(move)
			value = max(value, search(next, depth-1, alpha, beta, maximizing, personality))
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
		value = min(value, search(next, depth-1, alpha, beta, maximizing, personality))
		beta = min(beta, value)
		if alpha >= beta {
			break
		}
	}
	return value
}

func evaluate(state game.State, perspective game.Color, personality Personality) int {
	if state.Winner != 0 {
		if state.Winner == perspective {
			return 100000
		}
		return -100000
	}
	w := personalityWeights(personality)
	score := 0
	for index, piece := range state.Board {
		if piece == game.Empty {
			continue
		}
		row, col := index/8, index%8
		value := w.man
		if piece == game.RedKing || piece == game.BlackKing {
			value = w.king
		}
		if col == 0 || col == 7 {
			value += w.edge
		}
		if col >= 2 && col <= 5 && row >= 2 && row <= 5 {
			value += w.center
		}
		if piece == game.RedMan {
			value += (7 - row) * w.advance
		} else if piece == game.BlackMan {
			value += row * w.advance
		}
		isPerspective := (perspective == game.Red && (piece == game.RedMan || piece == game.RedKing)) ||
			(perspective == game.Black && (piece == game.BlackMan || piece == game.BlackKing))
		if isPerspective {
			score += value
		} else {
			score -= value
		}
	}
	mobility := len(state.LegalMoves()) * w.mobility
	if state.Turn == perspective {
		score += mobility
	} else {
		score -= mobility
	}
	return score
}

func personalityWeights(personality Personality) weights {
	switch personality {
	case Aggressive:
		return weights{man: 104, king: 182, edge: 2, advance: 9, center: 7, mobility: 5}
	case Defensive:
		return weights{man: 112, king: 188, edge: 18, advance: 3, center: 4, mobility: 2}
	case Trickster:
		return weights{man: 98, king: 190, edge: 5, advance: 7, center: 12, mobility: 7}
	default:
		return weights{man: 100, king: 175, edge: 10, advance: 6, center: 6, mobility: 3}
	}
}

func classify(loss int) string {
	switch {
	case loss <= 0:
		return "best"
	case loss < 15:
		return "good"
	case loss < 40:
		return "inaccuracy"
	case loss < 100:
		return "mistake"
	default:
		return "blunder"
	}
}

func pathKey(path []int) int {
	result := 0
	for _, square := range path {
		result = result*64 + square
	}
	return result
}

func samePath(left, right []int) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
