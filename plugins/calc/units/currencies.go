package units

import "strings"

// isoCodes are the ISO 4217 codes served by open.er-api.com (checked
// 2026-10-15). Codes are matched case-insensitively unless another unit
// has the same spelling ("cup" is a cooking cup, "CUP" the Cuban peso).
const isoCodes = "AED AFN ALL AMD ANG AOA ARS AUD AWG AZN BAM BBD BDT BGN BHD BIF BMD BND BOB BRL BSD BTN BWP BYN BZD " +
	"CAD CDF CHF CLF CLP CNH CNY COP CRC CUP CVE CZK DJF DKK DOP DZD EGP ERN ETB EUR FJD FKP FOK GBP GEL GGP GHS GIP " +
	"GMD GNF GTQ GYD HKD HNL HRK HTG HUF IDR ILS IMP INR IQD IRR ISK JEP JMD JOD JPY KES KGS KHR KID KMF KRW KWD KYD " +
	"KZT LAK LBP LKR LRD LSL LYD MAD MDL MGA MKD MMK MNT MOP MRU MUR MVR MWK MXN MYR MZN NAD NGN NIO NOK NPR NZD OMR " +
	"PAB PEN PGK PHP PKR PLN PYG QAR RON RSD RUB RWF SAR SBD SCR SDG SEK SGD SHP SLE SLL SOS SRD SSP STN SYP SZL THB " +
	"TJS TMT TND TOP TRY TTD TVD TWD TZS UAH UGX USD UYU UZS VES VND VUV WST XAF XCD XCG XDR XOF XPF YER ZAR ZMW ZWG ZWL"

// currencyNames are the symbols and names of common currencies.
var currencyNames = map[string]struct{ syms, names, zh []string }{
	"USD": {syms: []string{"$", "US$"}, names: []string{"dollar", "dollars", "us dollar", "us dollars", "buck", "bucks"}, zh: []string{"美元", "美金", "刀"}},
	"CNY": {syms: []string{"RMB", "rmb"}, names: []string{"yuan", "renminbi", "chinese yuan"}, zh: []string{"人民币", "元", "块", "块钱"}},
	"EUR": {syms: []string{"€"}, names: []string{"euro", "euros"}, zh: []string{"欧元"}},
	"JPY": {syms: []string{"円"}, names: []string{"yen", "japanese yen"}, zh: []string{"日元", "日币", "日圆"}},
	"GBP": {syms: []string{"£"}, names: []string{"pound sterling", "pounds sterling", "sterling", "quid"}, zh: []string{"英镑"}},
	"HKD": {syms: []string{"HK$"}, names: []string{"hong kong dollar", "hong kong dollars"}, zh: []string{"港币", "港元", "港纸"}},
	"TWD": {syms: []string{"NT$"}, names: []string{"new taiwan dollar", "taiwan dollar"}, zh: []string{"新台币", "台币"}},
	"KRW": {syms: []string{"₩"}, names: []string{"won", "korean won"}, zh: []string{"韩元", "韩币"}},
	"AUD": {syms: []string{"A$"}, names: []string{"australian dollar", "australian dollars"}, zh: []string{"澳元", "澳币"}},
	"CAD": {syms: []string{"C$"}, names: []string{"canadian dollar", "canadian dollars"}, zh: []string{"加元", "加币"}},
	"CHF": {names: []string{"swiss franc", "swiss francs"}, zh: []string{"瑞郎", "瑞士法郎"}},
	"SGD": {syms: []string{"S$"}, names: []string{"singapore dollar", "singapore dollars"}, zh: []string{"新加坡元", "新币"}},
	"INR": {syms: []string{"₹"}, names: []string{"rupee", "rupees", "indian rupee"}, zh: []string{"印度卢比", "卢比"}},
	"RUB": {syms: []string{"₽"}, names: []string{"ruble", "rubles", "rouble", "roubles"}, zh: []string{"卢布"}},
	"THB": {syms: []string{"฿"}, names: []string{"baht"}, zh: []string{"泰铢"}},
	"MOP": {names: []string{"pataca"}, zh: []string{"澳门元", "澳门币"}},
	"MYR": {names: []string{"ringgit"}, zh: []string{"马币", "林吉特"}},
	"VND": {syms: []string{"₫"}, names: []string{"dong"}, zh: []string{"越南盾"}},
	"NZD": {names: []string{"new zealand dollar"}, zh: []string{"新西兰元", "纽币"}},
	"PHP": {syms: []string{"₱"}, names: []string{"peso", "philippine peso"}, zh: []string{"菲律宾比索"}},
	"BRL": {syms: []string{"R$"}, names: []string{"real", "reais"}, zh: []string{"雷亚尔"}},
	"MXN": {names: []string{"mexican peso"}, zh: []string{"墨西哥比索"}},
	"TRY": {syms: []string{"₺"}, names: []string{"lira", "turkish lira"}, zh: []string{"土耳其里拉"}},
	"SEK": {names: []string{"swedish krona"}, zh: []string{"瑞典克朗"}},
	"NOK": {names: []string{"norwegian krone"}, zh: []string{"挪威克朗"}},
	"DKK": {names: []string{"danish krone"}, zh: []string{"丹麦克朗"}},
	"AED": {names: []string{"dirham"}, zh: []string{"迪拉姆"}},
	"SAR": {names: []string{"riyal"}, zh: []string{"里亚尔"}},
	"ZAR": {names: []string{"rand"}, zh: []string{"兰特"}},
}

// Codes returns the ISO 4217 codes the table knows.
func Codes() []string { return strings.Fields(isoCodes) }

// YenSymbol returns the currency code "¥" stands for, given the
// yenMeansJPY preference.
func YenSymbol(jpy bool) string {
	if jpy {
		return "JPY"
	}
	return "CNY"
}

// IsYen reports whether s is a yen sign, whose meaning is a preference.
func IsYen(s string) bool { return s == "¥" || s == "￥" }

func currencyDefs() []def {
	var d []def
	for _, code := range Codes() {
		n := currencyNames[code]
		d = append(d, def{sym: code, cat: CatCurrency, dim: dCurrency, dyn: DynCurrency, syms: n.syms, names: n.names, zh: n.zh})
	}
	return d
}
