package token

// EstimateText 按每四个 ASCII 字符、每个非 ASCII 字符约两个 token 粗估。
// 各模型的分词方式不同，此值仅用于本地预算检查。
func EstimateText(text string) int {
	ascii, other := 0, 0
	for _, r := range text {
		if r <= 127 {
			ascii++
		} else {
			other++
		}
	}
	return (ascii+3)/4 + other*2
}
