package advisor

import (
	"fmt"
	"math"

	"poker-game-analyzer/pkg/equity"
	"poker-game-analyzer/pkg/table"
)

type ActionRecommendation struct {
	Action      table.ActionType `json:"action"`
	Amount      float64          `json:"amount,omitempty"`
	EV          float64          `json:"ev"`
	IsPrimary   bool             `json:"is_primary"`
	SizingLabel string           `json:"sizing_label,omitempty"`
}

type AdvisorResponse struct {
	HandID            string                 `json:"hand_id"`
	HeroCards         [2]string              `json:"hero_cards"`
	Equity            float64                `json:"equity"`
	PotOdds           float64                `json:"pot_odds"`
	Actions           []ActionRecommendation `json:"actions"`
	PrimaryAction     table.ActionType       `json:"primary_action"`
	RecommendedAmount float64                `json:"recommended_amount"`
	Reasoning         string                 `json:"reasoning"`
}

func roundToTwoDecimals(val float64) float64 {
	return math.Round(val*100) / 100
}

func CalculateAdvice(state table.HandState, eq equity.EquityResult, oppTendencies map[string]float64) AdvisorResponse {
	pot := state.Pot
	if pot <= 0 {
		pot = 1.0
	}

	heroCurrentBet := 0.0
	heroStack := 0.0
	for _, seat := range state.Seats {
		if seat.PlayerID == state.HeroID && state.HeroID != "" {
			heroCurrentBet = seat.CurrentBet
			heroStack = seat.Stack
			break
		}
	}

	toCall := state.CurrentBet - heroCurrentBet
	if toCall < 0 {
		toCall = 0
	}
	if state.CurrentBet > 0 && toCall == 0 && heroCurrentBet == 0 {
		toCall = state.CurrentBet
	}

	potOdds := 0.0
	if toCall > 0 {
		potOdds = toCall / (pot + toCall)
	}

	winEq := eq.WinRate + eq.TieRate*0.5

	pFold := 0.35
	if oppTendencies != nil {
		if state.Street == table.StreetPreflop {
			if val, ok := oppTendencies["fold_to_3bet"]; ok && val >= 0 {
				pFold = val
			} else if val, ok := oppTendencies["fold_to_raise"]; ok && val >= 0 {
				pFold = val
			} else if val, ok := oppTendencies["fold_to_cbet"]; ok && val >= 0 {
				pFold = val
			} else {
				pFold = 0.40
			}
		} else if state.Street == table.StreetFlop {
			if val, ok := oppTendencies["fold_to_cbet"]; ok && val >= 0 {
				pFold = val
			} else if val, ok := oppTendencies["fold_to_bet"]; ok && val >= 0 {
				pFold = val
			} else if val, ok := oppTendencies["fold_to_raise"]; ok && val >= 0 {
				pFold = val
			}
		} else { // Turn or River
			if val, ok := oppTendencies["fold_to_cbet"]; ok && val >= 0 {
				pFold = val
			} else if val, ok := oppTendencies["fold_to_bet"]; ok && val >= 0 {
				pFold = val
			} else if val, ok := oppTendencies["fold_to_raise"]; ok && val >= 0 {
				pFold = val
			} else {
				pFold = 0.30
			}
		}
	}
	if pFold < 0.0 {
		pFold = 0.0
	} else if pFold > 1.0 {
		pFold = 1.0
	}

	evFold := 0.0

	var evCall float64
	if toCall == 0 {
		evCall = winEq * pot
	} else {
		evCall = winEq*(pot+toCall) - toCall
	}

	calcRaiseEV := func(raiseAmount float64) float64 {
		return pFold*pot + (1.0-pFold)*(winEq*(pot+2.0*raiseAmount)-raiseAmount)
	}

	if heroStack <= 0 {
		heroStack = math.Max(pot*3.0, math.Max(toCall*5.0, 100.0))
	}

	var actions []ActionRecommendation

	actions = append(actions, ActionRecommendation{
		Action:      table.ActionFold,
		Amount:      0,
		EV:          evFold,
		SizingLabel: "Fold",
	})

	if toCall == 0 {
		actions = append(actions, ActionRecommendation{
			Action:      table.ActionCheck,
			Amount:      0,
			EV:          evCall,
			SizingLabel: "Check",
		})

		betAction := table.ActionBet
		if state.Street == table.StreetPreflop {
			betAction = table.ActionRaise
		}

		s33 := roundToTwoDecimals(pot * 0.33)
		if s33 <= 0 {
			s33 = 1.0
		}

		s66 := roundToTwoDecimals(pot * 0.66)
		if s66 <= 0 {
			s66 = 2.0
		}

		s100 := roundToTwoDecimals(pot * 1.0)
		if s100 <= 0 {
			s100 = 3.0
		}

		allInAmt := roundToTwoDecimals(heroStack)

		actions = append(actions, ActionRecommendation{
			Action:      betAction,
			Amount:      s33,
			EV:          calcRaiseEV(s33),
			SizingLabel: "33% Pot",
		})

		actions = append(actions, ActionRecommendation{
			Action:      betAction,
			Amount:      s66,
			EV:          calcRaiseEV(s66),
			SizingLabel: "66% Pot",
		})

		actions = append(actions, ActionRecommendation{
			Action:      betAction,
			Amount:      s100,
			EV:          calcRaiseEV(s100),
			SizingLabel: "Pot",
		})

		actions = append(actions, ActionRecommendation{
			Action:      table.ActionAllIn,
			Amount:      allInAmt,
			EV:          calcRaiseEV(allInAmt),
			SizingLabel: "All-In",
		})
	} else {
		actions = append(actions, ActionRecommendation{
			Action:      table.ActionCall,
			Amount:      toCall,
			EV:          evCall,
			SizingLabel: "Call",
		})

		minRaise := state.MinRaise
		if minRaise < toCall*2.0 {
			minRaise = roundToTwoDecimals(toCall * 2.0)
		}

		s25x := roundToTwoDecimals(toCall * 2.5)
		if s25x < minRaise {
			s25x = minRaise
		}

		s66 := roundToTwoDecimals(toCall + pot*0.66)
		if s66 < minRaise {
			s66 = minRaise
		}

		sPot := roundToTwoDecimals(pot + 2.0*toCall)
		if sPot < minRaise {
			sPot = minRaise
		}

		allInAmt := roundToTwoDecimals(heroStack)
		if allInAmt < minRaise {
			allInAmt = minRaise
		}

		actions = append(actions, ActionRecommendation{
			Action:      table.ActionRaise,
			Amount:      minRaise,
			EV:          calcRaiseEV(minRaise),
			SizingLabel: "Min-Raise",
		})

		actions = append(actions, ActionRecommendation{
			Action:      table.ActionRaise,
			Amount:      s25x,
			EV:          calcRaiseEV(s25x),
			SizingLabel: "2.5x",
		})

		actions = append(actions, ActionRecommendation{
			Action:      table.ActionRaise,
			Amount:      s66,
			EV:          calcRaiseEV(s66),
			SizingLabel: "66% Pot",
		})

		actions = append(actions, ActionRecommendation{
			Action:      table.ActionRaise,
			Amount:      sPot,
			EV:          calcRaiseEV(sPot),
			SizingLabel: "Pot",
		})

		actions = append(actions, ActionRecommendation{
			Action:      table.ActionAllIn,
			Amount:      allInAmt,
			EV:          calcRaiseEV(allInAmt),
			SizingLabel: "All-In",
		})
	}

	// Determine the primary action:
	bestIdx := 0
	if toCall == 0 {
		// Check is default (index 1)
		bestIdx = 1
		bestEV := actions[1].EV

		// Consider betting if value (winEq >= 0.50) or high fold equity bluff (pFold >= 0.35)
		canBet := (winEq >= 0.50) || (pFold >= 0.35)
		if canBet {
			for i := 2; i < len(actions); i++ {
				act := actions[i]
				if act.Action != table.ActionAllIn && act.EV > bestEV {
					bestEV = act.EV
					bestIdx = i
				}
			}
		}
	} else {
		// Facing a bet:
		// Check if Raise is viable:
		// 1) Value raise: winEq >= 0.50
		// 2) Semi-bluff raise: winEq < 0.50 and pFold >= 0.35
		canRaise := (winEq >= 0.50) || (pFold >= 0.35)
		if state.Street == table.StreetRiver && winEq < 0.55 && pFold < 0.35 {
			canRaise = false
		}

		bestIdx = 0
		bestEV := evFold // 0.0

		if evCall > bestEV {
			bestEV = evCall
			bestIdx = 1 // Call
		}

		if canRaise {
			for i := 2; i < len(actions); i++ {
				act := actions[i]
				if act.Action != table.ActionAllIn && act.EV > bestEV {
					bestEV = act.EV
					bestIdx = i
				}
			}
		}
	}

	actions[bestIdx].IsPrimary = true
	primaryAct := actions[bestIdx]

	bluffFreq := 0.0
	if oppTendencies != nil {
		if bf, ok := oppTendencies["bluff_frequency"]; ok {
			bluffFreq = bf
		}
	}

	var reasoning string
	if primaryAct.Action == table.ActionRaise || primaryAct.Action == table.ActionBet || primaryAct.Action == table.ActionAllIn {
		if winEq >= 0.50 {
			reasoning = fmt.Sprintf("High equity (%.1f%%) > PotOdds (%.1f%%). Value %s to %.2f (%s) to extract value and exploit.", winEq*100, potOdds*100, primaryAct.Action, primaryAct.Amount, primaryAct.SizingLabel)
		} else if pFold >= 0.35 {
			reasoning = fmt.Sprintf("Profitable bluff/semi-bluff with %.1f%% fold equity and %.1f%% equity. %s to %.2f (%s).", pFold*100, winEq*100, primaryAct.Action, primaryAct.Amount, primaryAct.SizingLabel)
		} else {
			reasoning = fmt.Sprintf("Positive EV (+%.2f) %s to %.2f (%s) with %.1f%% equity against opponent range.", primaryAct.EV, primaryAct.Action, primaryAct.Amount, primaryAct.SizingLabel, winEq*100)
		}
	} else if primaryAct.Action == table.ActionCall {
		if bluffFreq >= 0.30 {
			reasoning = fmt.Sprintf("Profitable bluff catcher: Equity (%.1f%%) exceeds PotOdds (%.1f%%) against aggressive opponent (bluff freq %.0f%%). Call %.2f.", winEq*100, potOdds*100, bluffFreq*100, toCall)
		} else {
			reasoning = fmt.Sprintf("Sufficient equity (%.1f%%) for profitable call against PotOdds (%.1f%%). Call %.2f (EV: +%.2f).", winEq*100, potOdds*100, toCall, primaryAct.EV)
		}
	} else if primaryAct.Action == table.ActionCheck {
		reasoning = fmt.Sprintf("Free check with %.1f%% equity. Pot: %.2f.", winEq*100, pot)
	} else {
		reasoning = fmt.Sprintf("Equity (%.1f%%) insufficient for PotOdds (%.1f%%). Fold is optimal (EV: 0.00 vs Call EV: %.2f).", winEq*100, potOdds*100, evCall)
	}

	return AdvisorResponse{
		HandID:            state.HandID,
		HeroCards:         [2]string{state.HeroCards[0].String(), state.HeroCards[1].String()},
		Equity:            winEq,
		PotOdds:           potOdds,
		Actions:           actions,
		PrimaryAction:     primaryAct.Action,
		RecommendedAmount: primaryAct.Amount,
		Reasoning:         reasoning,
	}
}
