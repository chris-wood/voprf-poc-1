package ecgroup

import (
	"hash"
	"math/big"

	gg "github.com/alxdavids/oprf-poc/go/oprf/groups"
	"golang.org/x/crypto/hkdf"
)

// big.Int constants
var (
	zero, one, minusOne, two *big.Int = big.NewInt(0), big.NewInt(1), big.NewInt(-1), big.NewInt(2)
)

// h2cParams contains all of the parameters required for computing the
// hash_to_curve mapping algorithm, see
// https://tools.ietf.org/html/draft-irtf-cfrg-hash-to-curve-05 for more
// information.
type h2cParams struct {
	gc      GroupCurve
	dst     []byte
	mapping int
	z       int
	a       *big.Int
	b       *big.Int
	p       *big.Int
	m       int
	hash    hash.Hash
	l       int
	hEff    *big.Int
}

// getH2CParams returns the h2cParams object for the specified curve
func getH2CParams(gc GroupCurve) (h2cParams, error) {
	switch gc.Name() {
	case "P-384":
		return h2cParams{
			gc:      gc,
			dst:     []byte("VOPRF-P384-SHA512-SSWU-RO-"),
			mapping: 0,
			z:       -12,
			a:       big.NewInt(-3),
			b:       gc.ops.Params().B,
			p:       gc.Order(),
			m:       1,
			hash:    gc.Hash(),
			l:       72,
			hEff:    one,
		}, nil
	case "P-521":
		return h2cParams{
			gc:      gc,
			dst:     []byte("VOPRF-P521-SHA512-SSWU-RO-"),
			mapping: 0,
			z:       -4,
			a:       big.NewInt(-3),
			b:       gc.ops.Params().B,
			p:       gc.Order(),
			m:       1,
			hash:    gc.Hash(),
			l:       96,
			hEff:    one,
		}, nil
	}
	return h2cParams{}, gg.ErrUnsupportedGroup
}

// hashToBase hashes a buffer into a vector of underlying base field elements,
// where the base field is chosen depending on the associated elliptic curve
func (params h2cParams) hashToBaseField(buf []byte, ctr int) ([]*big.Int, error) {
	os, err := i2osp(0, 1)
	if err != nil {
		return nil, gg.ErrInternalInstantiation
	}
	hashFunc := func() hash.Hash { return params.hash }
	msgPrime := hkdf.Extract(hashFunc, params.dst, append(buf, os...))
	osCtr, err := i2osp(ctr, 1)
	if err != nil {
		return nil, gg.ErrInternalInstantiation
	}
	infoPfx := append([]byte("H2C"), osCtr...)
	i := 1
	res := make([]*big.Int, params.m)
	for i <= params.m {
		osi, err := i2osp(i, 1)
		if err != nil {
			return nil, gg.ErrInternalInstantiation
		}
		info := append(infoPfx, osi...)
		reader := hkdf.Expand(hashFunc, msgPrime, info)
		t := make([]byte, params.l)
		reader.Read(t)
		ei := int64(os2ip(t))
		res[i-1] = new(big.Int).Mod(big.NewInt(ei), params.p)
		i++
	}
	return res, nil
}

// hashToCurve hashes a buffer to a curve point on the chosen curve, this
// function can be modelled as a random oracle.
func (params h2cParams) hashToCurve(alpha []byte) (Point, error) {
	u0, err := params.hashToBaseField(alpha, 0)
	if err != nil {
		return Point{}, err
	}
	u1, err := params.hashToBaseField(alpha, 1)
	if err != nil {
		return Point{}, err
	}

	// attempt to encode bytes as curve point
	Q0 := Point{}
	Q1 := Point{}
	var e0, e1 error
	switch params.gc.Name() {
	case "P-384":
	case "P-521":
		Q0, e0 = params.sswu(u0)
		Q1, e1 = params.sswu(u1)
		break
	default:
		e0 = gg.ErrIncompatibleGroupParams
	}

	// return error if one occurred, or the point that was encoded
	if e0 != nil {
		return Point{}, e0
	} else if e1 != nil {
		return Point{}, e1
	}

	// construct the output point R
	R := Point{}
	err = R.Add(params.gc, Q0)
	if err != nil {
		return Point{}, err
	}
	err = R.Add(params.gc, Q1)
	if err != nil {
		return Point{}, err
	}
	err = R.clearCofactor(params.gc, params.hEff)
	if err != nil {
		return Point{}, err
	}
	return R, nil
}

// sswu completes the Simplified SWU method curve mapping defined in
// https://tools.ietf.org/html/draft-irtf-cfrg-hash-to-curve-05#section-6.6.2
func (params h2cParams) sswu(uArr []*big.Int) (Point, error) {
	if len(uArr) > 1 {
		return Point{}, gg.ErrIncompatibleGroupParams
	}
	u := uArr[0]
	p, A, B, Z := params.p, params.a, params.b, big.NewInt(int64(params.z))
	expRoot := new(big.Int).Mul(new(big.Int).Sub(p, one), new(big.Int).ModInverse(two, p))

	// consts
	// c1 := -B/A, c2 := -1/Z
	c1 := new(big.Int).Mod(new(big.Int).Mul(new(big.Int).Mul(B, minusOne), new(big.Int).ModInverse(A, p)), p)
	c2 := new(big.Int).Mul(minusOne, new(big.Int).ModInverse(Z, p))

	// steps
	t1 := new(big.Int).Mul(Z, new(big.Int).Exp(u, two, p))  // 1
	t2 := new(big.Int).Exp(t1, two, p)                      // 2
	x1 := new(big.Int).Add(t1, t2)                          // 3
	x1 = inv0(x1, p)                                        // 4
	e1 := new(big.Int).Abs(big.NewInt(int64(x1.Cmp(zero)))) // 5
	x1 = x1.Add(x1, one)                                    // 6
	x1 = cmov(x1, c2, e1)                                   // 7
	x1 = x1.Mul(x1, c1)                                     // 8
	gx1 := new(big.Int).Exp(x1, two, p)                     // 9
	gx1 = gx1.Add(gx1, A)                                   // 10
	gx1 = gx1.Mul(gx1, x1)                                  // 11
	gx1 = gx1.Add(gx1, B)                                   // 12
	x2 := new(big.Int).Mul(t1, x1)                          // 13
	t2 = t2.Mul(t1, t2)                                     // 14
	gx2 := new(big.Int).Mul(gx1, t2)                        // 15
	e2 := isSquare(gx1, expRoot, p)                         // 16
	x := cmov(x2, x1, e2)                                   // 17
	y2 := cmov(gx2, gx1, e2)                                // 18
	y := sqrt(y2, expRoot, p)                               // 19
	e3 := sgnCmp(u, y)                                      // 20
	y = cmov(y.Mul(y, minusOne), y, e3)                     // 21

	// construct point and assert that it is correct
	P := Point{X: x, Y: y}
	if !P.IsValid(params.gc) {
		return Point{}, gg.ErrInvalidGroupElement
	}
	return Point{X: x, Y: y}, nil
}

// returns 1 if the signs of s1 and s2 are the same, and 0 otherwise
func sgnCmp(s1, s2 *big.Int) *big.Int {
	c := new(big.Int).Abs(big.NewInt(int64(sgn0(s1).Cmp(sgn0(s2)))))
	return revCmpBit(c)
}

// sgn0 returns -1 if x is negative and 0/1 if x is positive
func sgn0(x *big.Int) *big.Int {
	c := int64(x.Cmp(zero))
	d := big.NewInt(c*2 + 2)
	// if c = 1 or 0 then d = 4 or 2, so e = 1
	// if c = -1 then d = 0, so e = -1
	e := int64(d.Cmp(one))
	return big.NewInt(e)
}

// sqrt computes the sqrt of x mod p (pass in exp explicitly so that we don't
// have to recompute)
func sqrt(x, exp, p *big.Int) *big.Int {
	return new(big.Int).Exp(x, exp, p)
}

// isSquare returns 1 if x is a square integer in FF_p and 0 otherwise, passes
// in the value exp to compute the square root in the exponent
func isSquare(x, exp, p *big.Int) *big.Int {
	b := sqrt(x, exp, p)
	c := b.Cmp(one)
	d := b.Cmp(zero)
	e := c * d
	f := new(big.Int).Abs(big.NewInt(int64(big.NewInt(int64(e)).Cmp(zero)))) // should be 0 if it is square, and 1 otherwise
	return revCmpBit(f)                                                      // returns 1 if square, and 0 otherwise
}

// revCmp reverses the result of a comparison bit indicator
func revCmpBit(cmp *big.Int) *big.Int {
	return new(big.Int).Mod(new(big.Int).Add(cmp, one), two)
}

// cmov is a constant-time big.Int conditional selector, returning b if c is 1,
// and a if c = 0
func cmov(a, b, c *big.Int) *big.Int {
	return new(big.Int).Add(new(big.Int).Mul(c, b), new(big.Int).Mul(new(big.Int).Sub(one, c), a))
}

// inv0 returns the inverse of x in FF_p, also returning 0^{-1} => 0
func inv0(x, p *big.Int) *big.Int {
	return x.Exp(x, new(big.Int).Sub(p, two), p)
}

// i2osp converts an integer to an octet-string
// (https://tools.ietf.org/html/rfc8017#section-4.1)
func i2osp(x, xLen int) ([]byte, error) {
	if x < 0 || x >= (1<<(8*xLen)) {
		return nil, gg.ErrInternalInstantiation
	}
	ret := make([]byte, xLen)
	val := x
	for i := xLen - 1; i >= 0; i-- {
		ret[i] = byte(val & 0xff)
		val = val >> 8
	}
	return ret, nil
}

// os2ip converts an octet-string to an integer
// (https://tools.ietf.org/html/rfc8017#section-4.1)
func os2ip(x []byte) int {
	ret := 0
	for _, b := range x {
		ret = ret << 8
		ret += int(b)
	}
	return ret
}
