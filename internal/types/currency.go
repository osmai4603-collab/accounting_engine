package types

import (
	"sort"
	"strings"
)

const (
	AED CurrencyCode = "AED"
	AFN CurrencyCode = "AFN"
	ALL CurrencyCode = "ALL"
	AMD CurrencyCode = "AMD"
	ANG CurrencyCode = "ANG"
	AOA CurrencyCode = "AOA"
	ARS CurrencyCode = "ARS"
	AUD CurrencyCode = "AUD"
	AWG CurrencyCode = "AWG"
	AZN CurrencyCode = "AZN"
	BAM CurrencyCode = "BAM"
	BBD CurrencyCode = "BBD"
	BDT CurrencyCode = "BDT"
	BGN CurrencyCode = "BGN"
	BHD CurrencyCode = "BHD"
	BIF CurrencyCode = "BIF"
	BMD CurrencyCode = "BMD"
	BND CurrencyCode = "BND"
	BOB CurrencyCode = "BOB"
	BOV CurrencyCode = "BOV"
	BRL CurrencyCode = "BRL"
	BSD CurrencyCode = "BSD"
	BTN CurrencyCode = "BTN"
	BWP CurrencyCode = "BWP"
	BYN CurrencyCode = "BYN"
	BZD CurrencyCode = "BZD"
	CAD CurrencyCode = "CAD"
	CDF CurrencyCode = "CDF"
	CHE CurrencyCode = "CHE"
	CHF CurrencyCode = "CHF"
	CHW CurrencyCode = "CHW"
	CLF CurrencyCode = "CLF"
	CLP CurrencyCode = "CLP"
	CNY CurrencyCode = "CNY"
	COP CurrencyCode = "COP"
	COU CurrencyCode = "COU"
	CRC CurrencyCode = "CRC"
	CUC CurrencyCode = "CUC"
	CUP CurrencyCode = "CUP"
	CVE CurrencyCode = "CVE"
	CZK CurrencyCode = "CZK"
	DJF CurrencyCode = "DJF"
	DKK CurrencyCode = "DKK"
	DOP CurrencyCode = "DOP"
	DZD CurrencyCode = "DZD"
	EGP CurrencyCode = "EGP"
	ERN CurrencyCode = "ERN"
	ETB CurrencyCode = "ETB"
	EUR CurrencyCode = "EUR"
	FJD CurrencyCode = "FJD"
	FKP CurrencyCode = "FKP"
	GBP CurrencyCode = "GBP"
	GEL CurrencyCode = "GEL"
	GHS CurrencyCode = "GHS"
	GIP CurrencyCode = "GIP"
	GMD CurrencyCode = "GMD"
	GNF CurrencyCode = "GNF"
	GTQ CurrencyCode = "GTQ"
	GYD CurrencyCode = "GYD"
	HKD CurrencyCode = "HKD"
	HNL CurrencyCode = "HNL"
	HTG CurrencyCode = "HTG"
	HUF CurrencyCode = "HUF"
	IDR CurrencyCode = "IDR"
	ILS CurrencyCode = "ILS"
	INR CurrencyCode = "INR"
	IQD CurrencyCode = "IQD"
	IRR CurrencyCode = "IRR"
	ISK CurrencyCode = "ISK"
	JMD CurrencyCode = "JMD"
	JOD CurrencyCode = "JOD"
	JPY CurrencyCode = "JPY"
	KES CurrencyCode = "KES"
	KGS CurrencyCode = "KGS"
	KHR CurrencyCode = "KHR"
	KMF CurrencyCode = "KMF"
	KPW CurrencyCode = "KPW"
	KRW CurrencyCode = "KRW"
	KWD CurrencyCode = "KWD"
	KYD CurrencyCode = "KYD"
	KZT CurrencyCode = "KZT"
	LAK CurrencyCode = "LAK"
	LBP CurrencyCode = "LBP"
	LKR CurrencyCode = "LKR"
	LRD CurrencyCode = "LRD"
	LSL CurrencyCode = "LSL"
	LYD CurrencyCode = "LYD"
	MAD CurrencyCode = "MAD"
	MDL CurrencyCode = "MDL"
	MGA CurrencyCode = "MGA"
	MKD CurrencyCode = "MKD"
	MMK CurrencyCode = "MMK"
	MNT CurrencyCode = "MNT"
	MOP CurrencyCode = "MOP"
	MRU CurrencyCode = "MRU"
	MUR CurrencyCode = "MUR"
	MVR CurrencyCode = "MVR"
	MWK CurrencyCode = "MWK"
	MXN CurrencyCode = "MXN"
	MXV CurrencyCode = "MXV"
	MYR CurrencyCode = "MYR"
	MZN CurrencyCode = "MZN"
	NAD CurrencyCode = "NAD"
	NGN CurrencyCode = "NGN"
	NIO CurrencyCode = "NIO"
	NOK CurrencyCode = "NOK"
	NPR CurrencyCode = "NPR"
	NZD CurrencyCode = "NZD"
	OMR CurrencyCode = "OMR"
	PAB CurrencyCode = "PAB"
	PEN CurrencyCode = "PEN"
	PGK CurrencyCode = "PGK"
	PHP CurrencyCode = "PHP"
	PKR CurrencyCode = "PKR"
	PLN CurrencyCode = "PLN"
	PYG CurrencyCode = "PYG"
	QAR CurrencyCode = "QAR"
	RON CurrencyCode = "RON"
	RSD CurrencyCode = "RSD"
	RUB CurrencyCode = "RUB"
	RWF CurrencyCode = "RWF"
	SAR CurrencyCode = "SAR"
	SBD CurrencyCode = "SBD"
	SCR CurrencyCode = "SCR"
	SDG CurrencyCode = "SDG"
	SEK CurrencyCode = "SEK"
	SGD CurrencyCode = "SGD"
	SHP CurrencyCode = "SHP"
	SLE CurrencyCode = "SLE"
	SLL CurrencyCode = "SLL"
	SOS CurrencyCode = "SOS"
	SRD CurrencyCode = "SRD"
	SSP CurrencyCode = "SSP"
	STN CurrencyCode = "STN"
	SVC CurrencyCode = "SVC"
	SYP CurrencyCode = "SYP"
	SZL CurrencyCode = "SZL"
	THB CurrencyCode = "THB"
	TJS CurrencyCode = "TJS"
	TMT CurrencyCode = "TMT"
	TND CurrencyCode = "TND"
	TOP CurrencyCode = "TOP"
	TRY CurrencyCode = "TRY"
	TTD CurrencyCode = "TTD"
	TWD CurrencyCode = "TWD"
	TZS CurrencyCode = "TZS"
	UAH CurrencyCode = "UAH"
	UGX CurrencyCode = "UGX"
	USD CurrencyCode = "USD"
	USN CurrencyCode = "USN"
	UYI CurrencyCode = "UYI"
	UYU CurrencyCode = "UYU"
	UYW CurrencyCode = "UYW"
	UZS CurrencyCode = "UZS"
	VED CurrencyCode = "VED"
	VES CurrencyCode = "VES"
	VND CurrencyCode = "VND"
	VUV CurrencyCode = "VUV"
	WST CurrencyCode = "WST"
	XAF CurrencyCode = "XAF"
	XCD CurrencyCode = "XCD"
	XDR CurrencyCode = "XDR"
	XOF CurrencyCode = "XOF"
	XPF CurrencyCode = "XPF"
	YER CurrencyCode = "YER"
	ZAR CurrencyCode = "ZAR"
	ZMW CurrencyCode = "ZMW"
	ZWG CurrencyCode = "ZWG"
	XXX CurrencyCode = "XXX"
)

type CurrencyCode string

type Currency struct {
	Code      CurrencyCode
	Symbol    string
	Name      string
	MinorUnit int32
}

var currencies = map[CurrencyCode]Currency{
	AED: {Code: AED, Symbol: "AED", Name: "UAE Dirham", MinorUnit: 2}, AFN: {Code: AFN, Symbol: "AFN", Name: "Afghani", MinorUnit: 2},
	ALL: {Code: ALL, Symbol: "ALL", Name: "Lek", MinorUnit: 2}, AMD: {Code: AMD, Symbol: "AMD", Name: "Armenian Dram", MinorUnit: 2},
	ANG: {Code: ANG, Symbol: "ANG", Name: "Netherlands Antillean Guilder", MinorUnit: 2}, AOA: {Code: AOA, Symbol: "AOA", Name: "Kwanza", MinorUnit: 2},
	ARS: {Code: ARS, Symbol: "ARS", Name: "Argentine Peso", MinorUnit: 2}, AUD: {Code: AUD, Symbol: "AUD", Name: "Australian Dollar", MinorUnit: 2},
	AWG: {Code: AWG, Symbol: "AWG", Name: "Aruban Florin", MinorUnit: 2}, AZN: {Code: AZN, Symbol: "AZN", Name: "Azerbaijan Manat", MinorUnit: 2},
	BAM: {Code: BAM, Symbol: "BAM", Name: "Convertible Mark", MinorUnit: 2}, BBD: {Code: BBD, Symbol: "BBD", Name: "Barbados Dollar", MinorUnit: 2},
	BDT: {Code: BDT, Symbol: "BDT", Name: "Taka", MinorUnit: 2}, BGN: {Code: BGN, Symbol: "BGN", Name: "Bulgarian Lev", MinorUnit: 2},
	BHD: {Code: BHD, Symbol: "BHD", Name: "Bahraini Dinar", MinorUnit: 3}, BIF: {Code: BIF, Symbol: "BIF", Name: "Burundi Franc", MinorUnit: 0},
	BMD: {Code: BMD, Symbol: "BMD", Name: "Bermudian Dollar", MinorUnit: 2}, BND: {Code: BND, Symbol: "BND", Name: "Brunei Dollar", MinorUnit: 2},
	BOB: {Code: BOB, Symbol: "BOB", Name: "Boliviano", MinorUnit: 2}, BOV: {Code: BOV, Symbol: "BOV", Name: "Mvdol", MinorUnit: 2},
	BRL: {Code: BRL, Symbol: "BRL", Name: "Brazilian Real", MinorUnit: 2}, BSD: {Code: BSD, Symbol: "BSD", Name: "Bahamian Dollar", MinorUnit: 2},
	BTN: {Code: BTN, Symbol: "BTN", Name: "Ngultrum", MinorUnit: 2}, BWP: {Code: BWP, Symbol: "BWP", Name: "Pula", MinorUnit: 2},
	BYN: {Code: BYN, Symbol: "BYN", Name: "Belarusian Ruble", MinorUnit: 2}, BZD: {Code: BZD, Symbol: "BZD", Name: "Belize Dollar", MinorUnit: 2},
	CAD: {Code: CAD, Symbol: "CAD", Name: "Canadian Dollar", MinorUnit: 2}, CDF: {Code: CDF, Symbol: "CDF", Name: "Congolese Franc", MinorUnit: 2},
	CHE: {Code: CHE, Symbol: "CHE", Name: "WIR Euro", MinorUnit: 2}, CHF: {Code: CHF, Symbol: "CHF", Name: "Swiss Franc", MinorUnit: 2},
	CHW: {Code: CHW, Symbol: "CHW", Name: "WIR Franc", MinorUnit: 2}, CLF: {Code: CLF, Symbol: "CLF", Name: "Unidad de Fomento", MinorUnit: 4},
	CLP: {Code: CLP, Symbol: "CLP", Name: "Chilean Peso", MinorUnit: 0}, CNY: {Code: CNY, Symbol: "CNY", Name: "Yuan Renminbi", MinorUnit: 2},
	COP: {Code: COP, Symbol: "COP", Name: "Colombian Peso", MinorUnit: 2}, COU: {Code: COU, Symbol: "COU", Name: "Unidad de Valor Real", MinorUnit: 2},
	CRC: {Code: CRC, Symbol: "CRC", Name: "Costa Rican Colon", MinorUnit: 2}, CUC: {Code: CUC, Symbol: "CUC", Name: "Peso Convertible", MinorUnit: 2},
	CUP: {Code: CUP, Symbol: "CUP", Name: "Cuban Peso", MinorUnit: 2}, CVE: {Code: CVE, Symbol: "CVE", Name: "Cabo Verde Escudo", MinorUnit: 2},
	CZK: {Code: CZK, Symbol: "CZK", Name: "Czech Koruna", MinorUnit: 2}, DJF: {Code: DJF, Symbol: "DJF", Name: "Djibouti Franc", MinorUnit: 0},
	DKK: {Code: DKK, Symbol: "DKK", Name: "Danish Krone", MinorUnit: 2}, DOP: {Code: DOP, Symbol: "DOP", Name: "Dominican Peso", MinorUnit: 2},
	DZD: {Code: DZD, Symbol: "DZD", Name: "Algerian Dinar", MinorUnit: 2}, EGP: {Code: EGP, Symbol: "EGP", Name: "Egyptian Pound", MinorUnit: 2},
	ERN: {Code: ERN, Symbol: "ERN", Name: "Nakfa", MinorUnit: 2}, ETB: {Code: ETB, Symbol: "ETB", Name: "Ethiopian Birr", MinorUnit: 2},
	EUR: {Code: EUR, Symbol: "EUR", Name: "Euro", MinorUnit: 2}, FJD: {Code: FJD, Symbol: "FJD", Name: "Fiji Dollar", MinorUnit: 2},
	FKP: {Code: FKP, Symbol: "FKP", Name: "Falkland Islands Pound", MinorUnit: 2}, GBP: {Code: GBP, Symbol: "GBP", Name: "Pound Sterling", MinorUnit: 2},
	GEL: {Code: GEL, Symbol: "GEL", Name: "Lari", MinorUnit: 2}, GHS: {Code: GHS, Symbol: "GHS", Name: "Ghana Cedi", MinorUnit: 2},
	GIP: {Code: GIP, Symbol: "GIP", Name: "Gibraltar Pound", MinorUnit: 2}, GMD: {Code: GMD, Symbol: "GMD", Name: "Dalasi", MinorUnit: 2},
	GNF: {Code: GNF, Symbol: "GNF", Name: "Guinean Franc", MinorUnit: 0}, GTQ: {Code: GTQ, Symbol: "GTQ", Name: "Quetzal", MinorUnit: 2},
	GYD: {Code: GYD, Symbol: "GYD", Name: "Guyana Dollar", MinorUnit: 2}, HKD: {Code: HKD, Symbol: "HKD", Name: "Hong Kong Dollar", MinorUnit: 2},
	HNL: {Code: HNL, Symbol: "HNL", Name: "Lempira", MinorUnit: 2}, HTG: {Code: HTG, Symbol: "HTG", Name: "Gourde", MinorUnit: 2},
	HUF: {Code: HUF, Symbol: "HUF", Name: "Forint", MinorUnit: 2}, IDR: {Code: IDR, Symbol: "IDR", Name: "Rupiah", MinorUnit: 2},
	ILS: {Code: ILS, Symbol: "ILS", Name: "New Israeli Sheqel", MinorUnit: 2}, INR: {Code: INR, Symbol: "INR", Name: "Indian Rupee", MinorUnit: 2},
	IQD: {Code: IQD, Symbol: "IQD", Name: "Iraqi Dinar", MinorUnit: 3}, IRR: {Code: IRR, Symbol: "IRR", Name: "Iranian Rial", MinorUnit: 2},
	ISK: {Code: ISK, Symbol: "ISK", Name: "Iceland Krona", MinorUnit: 0}, JMD: {Code: JMD, Symbol: "JMD", Name: "Jamaican Dollar", MinorUnit: 2},
	JOD: {Code: JOD, Symbol: "JOD", Name: "Jordanian Dinar", MinorUnit: 3}, JPY: {Code: JPY, Symbol: "JPY", Name: "Yen", MinorUnit: 0},
	KES: {Code: KES, Symbol: "KES", Name: "Kenyan Shilling", MinorUnit: 2}, KGS: {Code: KGS, Symbol: "KGS", Name: "Som", MinorUnit: 2},
	KHR: {Code: KHR, Symbol: "KHR", Name: "Riel", MinorUnit: 2}, KMF: {Code: KMF, Symbol: "KMF", Name: "Comorian Franc", MinorUnit: 0},
	KPW: {Code: KPW, Symbol: "KPW", Name: "North Korean Won", MinorUnit: 2}, KRW: {Code: KRW, Symbol: "KRW", Name: "Won", MinorUnit: 0},
	KWD: {Code: KWD, Symbol: "KWD", Name: "Kuwaiti Dinar", MinorUnit: 3}, KYD: {Code: KYD, Symbol: "KYD", Name: "Cayman Islands Dollar", MinorUnit: 2},
	KZT: {Code: KZT, Symbol: "KZT", Name: "Tenge", MinorUnit: 2}, LAK: {Code: LAK, Symbol: "LAK", Name: "Lao Kip", MinorUnit: 2},
	LBP: {Code: LBP, Symbol: "LBP", Name: "Lebanese Pound", MinorUnit: 2}, LKR: {Code: LKR, Symbol: "LKR", Name: "Sri Lanka Rupee", MinorUnit: 2},
	LRD: {Code: LRD, Symbol: "LRD", Name: "Liberian Dollar", MinorUnit: 2}, LSL: {Code: LSL, Symbol: "LSL", Name: "Loti", MinorUnit: 2},
	LYD: {Code: LYD, Symbol: "LYD", Name: "Libyan Dinar", MinorUnit: 3}, MAD: {Code: MAD, Symbol: "MAD", Name: "Moroccan Dirham", MinorUnit: 2},
	MDL: {Code: MDL, Symbol: "MDL", Name: "Moldovan Leu", MinorUnit: 2}, MGA: {Code: MGA, Symbol: "MGA", Name: "Malagasy Ariary", MinorUnit: 2},
	MKD: {Code: MKD, Symbol: "MKD", Name: "Denar", MinorUnit: 2}, MMK: {Code: MMK, Symbol: "MMK", Name: "Kyat", MinorUnit: 2},
	MNT: {Code: MNT, Symbol: "MNT", Name: "Tugrik", MinorUnit: 2}, MOP: {Code: MOP, Symbol: "MOP", Name: "Pataca", MinorUnit: 2},
	MRU: {Code: MRU, Symbol: "MRU", Name: "Ouguiya", MinorUnit: 2}, MUR: {Code: MUR, Symbol: "MUR", Name: "Mauritius Rupee", MinorUnit: 2},
	MVR: {Code: MVR, Symbol: "MVR", Name: "Rufiyaa", MinorUnit: 2}, MWK: {Code: MWK, Symbol: "MWK", Name: "Malawi Kwacha", MinorUnit: 2},
	MXN: {Code: MXN, Symbol: "MXN", Name: "Mexican Peso", MinorUnit: 2}, MXV: {Code: MXV, Symbol: "MXV", Name: "Mexican Unidad de Inversion", MinorUnit: 2},
	MYR: {Code: MYR, Symbol: "MYR", Name: "Malaysian Ringgit", MinorUnit: 2}, MZN: {Code: MZN, Symbol: "MZN", Name: "Mozambique Metical", MinorUnit: 2},
	NAD: {Code: NAD, Symbol: "NAD", Name: "Namibia Dollar", MinorUnit: 2}, NGN: {Code: NGN, Symbol: "NGN", Name: "Naira", MinorUnit: 2},
	NIO: {Code: NIO, Symbol: "NIO", Name: "Cordoba Oro", MinorUnit: 2}, NOK: {Code: NOK, Symbol: "NOK", Name: "Norwegian Krone", MinorUnit: 2},
	NPR: {Code: NPR, Symbol: "NPR", Name: "Nepalese Rupee", MinorUnit: 2}, NZD: {Code: NZD, Symbol: "NZD", Name: "New Zealand Dollar", MinorUnit: 2},
	OMR: {Code: OMR, Symbol: "OMR", Name: "Rial Omani", MinorUnit: 3}, PAB: {Code: PAB, Symbol: "PAB", Name: "Balboa", MinorUnit: 2},
	PEN: {Code: PEN, Symbol: "PEN", Name: "Sol", MinorUnit: 2}, PGK: {Code: PGK, Symbol: "PGK", Name: "Kina", MinorUnit: 2},
	PHP: {Code: PHP, Symbol: "PHP", Name: "Philippine Peso", MinorUnit: 2}, PKR: {Code: PKR, Symbol: "PKR", Name: "Pakistan Rupee", MinorUnit: 2},
	PLN: {Code: PLN, Symbol: "PLN", Name: "Zloty", MinorUnit: 2}, PYG: {Code: PYG, Symbol: "PYG", Name: "Guarani", MinorUnit: 0},
	QAR: {Code: QAR, Symbol: "QAR", Name: "Qatari Rial", MinorUnit: 2}, RON: {Code: RON, Symbol: "RON", Name: "Romanian Leu", MinorUnit: 2},
	RSD: {Code: RSD, Symbol: "RSD", Name: "Serbian Dinar", MinorUnit: 2}, RUB: {Code: RUB, Symbol: "RUB", Name: "Russian Ruble", MinorUnit: 2},
	RWF: {Code: RWF, Symbol: "RWF", Name: "Rwanda Franc", MinorUnit: 0}, SAR: {Code: SAR, Symbol: "SAR", Name: "Saudi Riyal", MinorUnit: 2},
	SBD: {Code: SBD, Symbol: "SBD", Name: "Solomon Islands Dollar", MinorUnit: 2}, SCR: {Code: SCR, Symbol: "SCR", Name: "Seychelles Rupee", MinorUnit: 2},
	SDG: {Code: SDG, Symbol: "SDG", Name: "Sudanese Pound", MinorUnit: 2}, SEK: {Code: SEK, Symbol: "SEK", Name: "Swedish Krona", MinorUnit: 2},
	SGD: {Code: SGD, Symbol: "SGD", Name: "Singapore Dollar", MinorUnit: 2}, SHP: {Code: SHP, Symbol: "SHP", Name: "Saint Helena Pound", MinorUnit: 2},
	SLE: {Code: SLE, Symbol: "SLE", Name: "Leone", MinorUnit: 2}, SLL: {Code: SLL, Symbol: "SLL", Name: "Leone", MinorUnit: 2},
	SOS: {Code: SOS, Symbol: "SOS", Name: "Somali Shilling", MinorUnit: 2}, SRD: {Code: SRD, Symbol: "SRD", Name: "Surinam Dollar", MinorUnit: 2},
	SSP: {Code: SSP, Symbol: "SSP", Name: "South Sudanese Pound", MinorUnit: 2}, STN: {Code: STN, Symbol: "STN", Name: "Dobra", MinorUnit: 2},
	SVC: {Code: SVC, Symbol: "SVC", Name: "El Salvador Colon", MinorUnit: 2}, SYP: {Code: SYP, Symbol: "SYP", Name: "Syrian Pound", MinorUnit: 2},
	SZL: {Code: SZL, Symbol: "SZL", Name: "Lilangeni", MinorUnit: 2}, THB: {Code: THB, Symbol: "THB", Name: "Baht", MinorUnit: 2},
	TJS: {Code: TJS, Symbol: "TJS", Name: "Somoni", MinorUnit: 2}, TMT: {Code: TMT, Symbol: "TMT", Name: "Turkmenistan New Manat", MinorUnit: 2},
	TND: {Code: TND, Symbol: "TND", Name: "Tunisian Dinar", MinorUnit: 3}, TOP: {Code: TOP, Symbol: "TOP", Name: "Pa'anga", MinorUnit: 2},
	TRY: {Code: TRY, Symbol: "TRY", Name: "Turkish Lira", MinorUnit: 2}, TTD: {Code: TTD, Symbol: "TTD", Name: "Trinidad and Tobago Dollar", MinorUnit: 2},
	TWD: {Code: TWD, Symbol: "TWD", Name: "New Taiwan Dollar", MinorUnit: 2}, TZS: {Code: TZS, Symbol: "TZS", Name: "Tanzanian Shilling", MinorUnit: 2},
	UAH: {Code: UAH, Symbol: "UAH", Name: "Hryvnia", MinorUnit: 2}, UGX: {Code: UGX, Symbol: "UGX", Name: "Uganda Shilling", MinorUnit: 0},
	USD: {Code: USD, Symbol: "USD", Name: "US Dollar", MinorUnit: 2}, USN: {Code: USN, Symbol: "USN", Name: "US Dollar (Next day)", MinorUnit: 2},
	UYI: {Code: UYI, Symbol: "UYI", Name: "Uruguay Peso en Unidades Indexadas", MinorUnit: 0}, UYU: {Code: UYU, Symbol: "UYU", Name: "Peso Uruguayo", MinorUnit: 2},
	UYW: {Code: UYW, Symbol: "UYW", Name: "Unidad Previsional", MinorUnit: 4}, UZS: {Code: UZS, Symbol: "UZS", Name: "Uzbekistan Sum", MinorUnit: 2},
	VED: {Code: VED, Symbol: "VED", Name: "Bolivar Soberano", MinorUnit: 2}, VES: {Code: VES, Symbol: "VES", Name: "Bolivar Soberano", MinorUnit: 2},
	VND: {Code: VND, Symbol: "VND", Name: "Dong", MinorUnit: 0}, VUV: {Code: VUV, Symbol: "VUV", Name: "Vatu", MinorUnit: 0},
	WST: {Code: WST, Symbol: "WST", Name: "Tala", MinorUnit: 2}, XAF: {Code: XAF, Symbol: "XAF", Name: "CFA Franc BEAC", MinorUnit: 0},
	XCD: {Code: XCD, Symbol: "XCD", Name: "East Caribbean Dollar", MinorUnit: 2}, XDR: {Code: XDR, Symbol: "XDR", Name: "SDR", MinorUnit: 0},
	XOF: {Code: XOF, Symbol: "XOF", Name: "CFA Franc BCEAO", MinorUnit: 0}, XPF: {Code: XPF, Symbol: "XPF", Name: "CFP Franc", MinorUnit: 0},
	YER: {Code: YER, Symbol: "YER", Name: "Yemeni Rial", MinorUnit: 2}, ZAR: {Code: ZAR, Symbol: "ZAR", Name: "Rand", MinorUnit: 2},
	ZMW: {Code: ZMW, Symbol: "ZMW", Name: "Zambian Kwacha", MinorUnit: 2}, ZWG: {Code: ZWG, Symbol: "ZWG", Name: "Zimbabwe Gold", MinorUnit: 2},
	XXX: {Code: XXX, Symbol: "", Name: "No currency", MinorUnit: 0},
}

func LookupCurrency(code string) (Currency, bool) {
	c, ok := currencies[CurrencyCode(strings.ToUpper(strings.TrimSpace(code)))]
	return c, ok
}

func AllCurrencies() []Currency {
	result := make([]Currency, 0, len(currencies))
	for _, currency := range currencies {
		result = append(result, currency)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Code < result[j].Code })
	return result
}

func normalizeCurrency(code string) (string, error) {
	normalized := strings.ToUpper(strings.TrimSpace(code))
	if _, ok := LookupCurrency(normalized); !ok {
		return "", ErrInvalidCurrency
	}
	return normalized, nil
}
