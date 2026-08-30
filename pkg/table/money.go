package table

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Money is an exact amount, held as a whole number of minor units.
//
// Everything on a poker table is a fixed-point quantity: a stake, a stack, a
// pot, a wager. Held as float64 they are only nearly themselves -- 0.1 has no
// binary representation, and a pot accumulated over a hand drifts away from any
// figure a player could read off the screen. Held as an integer count of a
// small unit they are exactly themselves, and equality means equality.
//
// Expected value is not one of these quantities. It is a probability times an
// amount, and probabilities are real numbers; forcing it into fixed point buys
// nothing and costs precision in the one place precision is actually about
// arithmetic rather than about money. So the model converts to float64, works
// there, and quantises what it returns.
type Money int64

// MoneyScale is how many minor units make up one unit of currency.
//
// Four places, which covers every stake this client offers with room to spare:
// the smallest big blind seen in the wild is 0.01, and a fifth of a small blind
// still lands on a whole unit. int64 at this scale holds nine hundred trillion
// units of currency, so no stack can overflow it.
const MoneyScale = 10000

// Zero is the absent amount. It is also the zero value of the type, which is
// what makes an unset field read as "nothing wagered" rather than as garbage.
const Zero Money = 0

// FromFloat converts an amount to exact money, rounding to the nearest minor
// unit. Used at the edges: what the screen reader produced, and what the EV
// model came back with.
func FromFloat(v float64) Money {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return Zero
	}
	return Money(math.Round(v * MoneyScale))
}

// Float is the amount as a real number, for the arithmetic that is genuinely
// real: equity, expected value, pot odds, fractions of a pot.
func (m Money) Float() float64 { return float64(m) / MoneyScale }

// IsZero reports whether nothing is there. Worth its own name because "no bet"
// and "a bet of nothing" are the same value and different statements.
func (m Money) IsZero() bool { return m == 0 }

// Add, Sub and Scale keep exact amounts exact. Scaling by a real number -- two
// thirds of a pot, say -- rounds to the nearest minor unit, because a bet of
// two thirds of a pot is still a bet of some payable amount.
func (m Money) Add(o Money) Money { return m + o }
func (m Money) Sub(o Money) Money { return m - o }
func (m Money) Scale(f float64) Money {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return Zero
	}
	return Money(math.Round(float64(m) * f))
}

// String renders the amount the way it is written on the table: no trailing
// zeros beyond what the amount actually has, and no exponent.
func (m Money) String() string {
	neg := m < 0
	v := m
	if neg {
		v = -v
	}
	whole := int64(v) / MoneyScale
	frac := int64(v) % MoneyScale

	out := strconv.FormatInt(whole, 10)
	if frac != 0 {
		f := strings.TrimRight(fmt.Sprintf("%04d", frac), "0")
		out += "." + f
	}
	if neg {
		out = "-" + out
	}
	return out
}

// ParseMoney reads an exact amount from text without going through float64.
//
// Going through float64 is the one thing this type exists to avoid: parsing
// "0.10" to a float and multiplying by the scale can land a unit either side of
// the intended thousand.
func ParseMoney(s string) (Money, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Zero, errors.New("empty amount")
	}

	neg := false
	switch s[0] {
	case '+':
		s = s[1:]
	case '-':
		neg = true
		s = s[1:]
	}

	whole, frac, hasFrac := strings.Cut(s, ".")
	if whole == "" && !hasFrac {
		return Zero, fmt.Errorf("invalid amount %q", s)
	}
	if whole == "" {
		whole = "0"
	}

	w, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return Zero, fmt.Errorf("invalid amount %q: %w", s, err)
	}

	var f int64
	if hasFrac {
		// More places than the scale holds are rounded, not truncated: a stake
		// the client prints to more precision than we keep should land on the
		// nearest unit rather than always downwards.
		digits := frac
		for _, r := range digits {
			if r < '0' || r > '9' {
				return Zero, fmt.Errorf("invalid amount %q", s)
			}
		}
		const places = 4 // log10(MoneyScale)
		if len(digits) > places {
			roundUp := digits[places] >= '5'
			digits = digits[:places]
			f, err = strconv.ParseInt(digits, 10, 64)
			if err != nil {
				return Zero, fmt.Errorf("invalid amount %q: %w", s, err)
			}
			if roundUp {
				f++
			}
		} else {
			padded := digits + strings.Repeat("0", places-len(digits))
			f, err = strconv.ParseInt(padded, 10, 64)
			if err != nil {
				return Zero, fmt.Errorf("invalid amount %q: %w", s, err)
			}
		}
	}

	m := Money(w*MoneyScale + f)
	if neg {
		m = -m
	}
	return m, nil
}

// MarshalJSON writes the amount as a JSON number, so nothing downstream -- the
// HUD, the audit log, a hand history already on disk -- has to learn a new
// shape. It is exact on the way out because the digits are produced from the
// integer, never from a float.
func (m Money) MarshalJSON() ([]byte, error) {
	return []byte(m.String()), nil
}

// UnmarshalJSON accepts a number or a string, and parses the text rather than
// the float. A JSON number is decimal text on the wire; reading it as text is
// what keeps "0.1" from becoming 0.09999999999999999.
func (m *Money) UnmarshalJSON(data []byte) error {
	s := strings.TrimSpace(string(data))
	if s == "null" {
		*m = Zero
		return nil
	}
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		var str string
		if err := json.Unmarshal(data, &str); err != nil {
			return err
		}
		s = strings.TrimSpace(str)
		if s == "" {
			*m = Zero
			return nil
		}
	}
	// Exponent form is not something this client ever writes, but a hand
	// history round-tripped through another tool might. Falling back through
	// float64 there is a deliberate, narrow concession.
	if strings.ContainsAny(s, "eE") {
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return err
		}
		*m = FromFloat(f)
		return nil
	}
	parsed, err := ParseMoney(s)
	if err != nil {
		return err
	}
	*m = parsed
	return nil
}
