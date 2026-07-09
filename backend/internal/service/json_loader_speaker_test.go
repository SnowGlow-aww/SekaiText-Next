package service

import "testing"

func TestCharIndexForSpeaker(t *testing.T) {
	// AppearCharacters from a real event scenario (event_210_01 / event_208_02):
	// 296=ena, 291=emu, 292=nene, 346=sub_sakaki(配角), 100=mob(路人).
	c2d := map[int]int{}
	for id, costume := range map[int]string{
		296: "v2_19ena_casual",
		291: "v2_14emu_unit",
		292: "v2_15nene_casual",
		346: "sub_sakaki_black",
		100: "mob001",
	} {
		c2d[id] = charIndexForCostume(costume)
	}

	cases := []struct {
		speaker string
		chara2d int
		want    int
	}{
		{"絵名", 0, 18},
		{"絵名の声", 296, 18},        // 画外音：模型直出
		{"絵名の声", 0, 18},          // 无语音也能靠名字前缀
		{"奏のメッセージ", 0, 16},      // 无 Voices 的消息行
		{"みのりのスマホ", 0, 4},       // 名字本身含「の」也不误切
		{"子供の神使", 291, 13},       // 剧中剧角色名：跟着配音模型走
		{"穂波の母", 100, -1},        // mob 模型阻断名字前缀误匹配
		{"サカキ", 346, -1},          // sub 配角无头像
		{"司・えむ・寧々・類", 900000, 12}, // 群体台词：取第一个
		{"瑞希＆絵名", 900000, 19},
		{"？？？", 292, -1}, // 隐藏身份：即使带模型也不剧透
		{"ミク", 0, 20},
		{"場内アナウンスの声", 0, -1},
		{"", 0, -1},
	}
	for _, c := range cases {
		if got := charIndexForSpeaker(c.speaker, c.chara2d, c2d); got != c.want {
			t.Errorf("charIndexForSpeaker(%q, %d) = %d, want %d", c.speaker, c.chara2d, got, c.want)
		}
	}
}

func TestCharIndexForCostume(t *testing.T) {
	cases := []struct {
		costume string
		want    int
	}{
		{"v2_19ena_casual", 18},
		{"v2_21miku_night", 20},
		{"v1_06airi_normal", 6},
		{"v2_13tsukasa_unit", 12},
		{"sub_sakaki_black", -1},
		{"mob001", -1},
		{"", -1},
	}
	for _, c := range cases {
		if got := charIndexForCostume(c.costume); got != c.want {
			t.Errorf("charIndexForCostume(%q) = %d, want %d", c.costume, got, c.want)
		}
	}
}
