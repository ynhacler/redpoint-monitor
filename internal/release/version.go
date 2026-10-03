package release

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Version 是语义化版本（vMAJOR.MINOR.PATCH[-pre]），用于防降级比较（设计 29.7.4）。
//
// 开发构建的版本来自 git describe，形如 v0.2.0-3-gabc1234(-dirty)，表示 v0.2.0 之后的第 3 个提交：
// 按“在 v0.2.0 之后、v0.2.1 之前”比较，而不是当作 v0.2.0 的预发布版本。
type Version struct {
	Major, Minor, Patch int
	Pre                 string // 预发布标识，如 rc.1；空表示正式版
	post                bool   // git describe 的提交后缀
}

var (
	versionPattern  = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z.-]+))?$`)
	describePattern = regexp.MustCompile(`^\d+-g[0-9a-f]+(-dirty)?$`)
)

// ParseVersion 解析版本号；不符合语义化版本格式时返回错误。
func ParseVersion(s string) (Version, error) {
	m := versionPattern.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return Version{}, fmt.Errorf("版本号格式无效：%q", s)
	}
	var v Version
	v.Major, _ = strconv.Atoi(m[1])
	v.Minor, _ = strconv.Atoi(m[2])
	v.Patch, _ = strconv.Atoi(m[3])
	if describePattern.MatchString(m[4]) {
		v.post = true
	} else {
		v.Pre = m[4]
	}
	return v, nil
}

// Compare 返回 -1、0、1。预发布版本低于同号正式版；git describe 的提交后缀高于同号正式版。
func (a Version) Compare(b Version) int {
	for _, d := range []int{a.Major - b.Major, a.Minor - b.Minor, a.Patch - b.Patch} {
		if d != 0 {
			return sign(d)
		}
	}
	rank := func(v Version) int {
		switch {
		case v.post:
			return 2
		case v.Pre == "":
			return 1
		}
		return 0
	}
	if d := rank(a) - rank(b); d != 0 {
		return sign(d)
	}
	return sign(strings.Compare(a.Pre, b.Pre))
}

func (v Version) String() string {
	s := fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	if v.Pre != "" {
		s += "-" + v.Pre
	}
	return s
}

func sign(d int) int {
	switch {
	case d < 0:
		return -1
	case d > 0:
		return 1
	}
	return 0
}
