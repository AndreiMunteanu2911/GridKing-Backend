package bot

import (
	"errors"
	"hash/fnv"
	"time"

	"GridKing-Backend/internal/game"
)

type GeneratedPuzzle struct {
	State      game.State
	BestMove   game.Move
	Difficulty string
	ScoreGap   int
}

func GenerateDailyPuzzle(day time.Time) (GeneratedPuzzle, error) {
	seed := dailySeed(day)
	state := game.NewState()
	var fallback GeneratedPuzzle
	bestGap := -1
	for ply := 0; ply < 54 && state.Winner == 0; ply++ {
		playCandidates := RankMoves(state, 2, Balanced)
		if len(playCandidates) == 0 {
			break
		}
		if ply >= 14 && ply%2 == 0 {
			ranked := RankMoves(state, 5, Balanced)
			if len(ranked) >= 2 {
				gap := ranked[0].Score - ranked[1].Score
				if gap > bestGap {
					bestGap = gap
					fallback = GeneratedPuzzle{State: state, BestMove: ranked[0].Move, Difficulty: "hard", ScoreGap: gap}
				}
				if gap >= 30 && gap <= 5000 {
					difficulty := "medium"
					if gap < 70 {
						difficulty = "hard"
					} else if gap > 180 {
						difficulty = "easy"
					}
					return GeneratedPuzzle{State: state, BestMove: ranked[0].Move, Difficulty: difficulty, ScoreGap: gap}, nil
				}
			}
		}
		choice := int((seed + uint64(ply*17)) % uint64(min(3, len(playCandidates))))
		next, err := state.Apply(playCandidates[choice].Move)
		if err != nil {
			return GeneratedPuzzle{}, err
		}
		state = next
		seed = seed*6364136223846793005 + 1442695040888963407
	}
	if bestGap >= 0 {
		return fallback, nil
	}
	return GeneratedPuzzle{}, errors.New("could not generate a clear puzzle for this date")
}

func dailySeed(day time.Time) uint64 {
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(day.UTC().Format("2006-01-02")))
	return hash.Sum64()
}
