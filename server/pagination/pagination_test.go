// Author: tan91
// GitHub: https://github.com/NUDTTAN91
// Blog: https://blog.csdn.net/ZXW_NUDT

package pagination

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// newCtx 构造一个只带查询串的 gin.Context
func newCtx(rawQuery string) *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/?"+rawQuery, nil)
	return c
}

func TestParseDisabledWhenNoParams(t *testing.T) {
	// 不传 page / pageSize 时必须保持全量返回，否则会破坏尚未适配分页的页面
	p := Parse(newCtx(""), 20, 200)
	if p.Enabled {
		t.Fatalf("没有分页参数时 Enabled 应为 false，实际 %+v", p)
	}

	p = Parse(newCtx("type=static_container&categoryId=3"), 20, 200)
	if p.Enabled {
		t.Fatalf("只有过滤参数时 Enabled 应为 false，实际 %+v", p)
	}
}

func TestParse(t *testing.T) {
	cases := []struct {
		name       string
		query      string
		wantPage   int
		wantSize   int
		wantOffset int
	}{
		{"首页", "page=1", 1, 20, 0},
		{"第三页", "page=3", 3, 20, 40},
		{"自定义每页条数", "page=2&pageSize=50", 2, 50, 50},
		{"只传 pageSize 也启用分页", "pageSize=10", 1, 10, 0},
		{"page 为 0 回退到第一页", "page=0", 1, 20, 0},
		{"page 为负数回退到第一页", "page=-5", 1, 20, 0},
		{"page 非数字回退到第一页", "page=abc", 1, 20, 0},
		{"pageSize 超上限回退默认值", "page=1&pageSize=9999", 1, 20, 0},
		{"pageSize 为 0 回退默认值", "page=1&pageSize=0", 1, 20, 0},
		{"pageSize 非数字回退默认值", "page=2&pageSize=xyz", 2, 20, 20},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := Parse(newCtx(tc.query), 20, 200)
			if !p.Enabled {
				t.Fatalf("%q 应启用分页", tc.query)
			}
			if p.Page != tc.wantPage || p.PageSize != tc.wantSize || p.Offset != tc.wantOffset {
				t.Errorf("%q => page=%d size=%d offset=%d, want page=%d size=%d offset=%d",
					tc.query, p.Page, p.PageSize, p.Offset, tc.wantPage, tc.wantSize, tc.wantOffset)
			}
		})
	}
}

func TestTotalPages(t *testing.T) {
	p := Parse(newCtx("page=1&pageSize=20"), 20, 200)

	cases := map[int]int{
		0:  0,
		1:  1,
		20: 1,
		21: 2,
		40: 2,
		41: 3,
	}
	for total, want := range cases {
		if got := p.TotalPages(total); got != want {
			t.Errorf("total=%d => %d 页, want %d 页", total, got, want)
		}
	}

	// 未启用分页时不应参与页码计算
	if got := (Params{}).TotalPages(100); got != 0 {
		t.Errorf("未启用分页时 TotalPages 应为 0，实际 %d", got)
	}
}
