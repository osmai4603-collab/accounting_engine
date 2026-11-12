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
	Code        CurrencyCode
	NumericCode string
	Symbol      string
	Name        string
	MinorUnit   int32
}

var currencies = map[CurrencyCode]Currency{
	AED: {Code: AED, NumericCode: "784", Symbol: "AED", Name: "UAE Dirham", MinorUnit: 2}, AFN: {Code: AFN, NumericCode: "971", Symbol: "AFN", Name: "Afghani", MinorUnit: 2},
	ALL: {Code: ALL, NumericCode: "008", Symbol: "ALL", Name: "Lek", MinorUnit: 2}, AMD: {Code: AMD, NumericCode: "051", Symbol: "AMD", Name: "Armenian Dram", MinorUnit: 2},
	ANG: {Code: ANG, NumericCode: "532", Symbol: "ANG", Name: "Netherlands Antillean Guilder", MinorUnit: 2}, AOA: {Code: AOA, NumericCode: "973", Symbol: "AOA", Name: "Kwanza", MinorUnit: 2},
	ARS: {Code: ARS, NumericCode: "032", Symbol: "ARS", Name: "Argentine Peso", MinorUnit: 2}, AUD: {Code: AUD, NumericCode: "036", Symbol: "AUD", Name: "Australian Dollar", MinorUnit: 2},
	AWG: {Code: AWG, NumericCode: "533", Symbol: "AWG", Name: "Aruban Florin", MinorUnit: 2}, AZN: {Code: AZN, NumericCode: "944", Symbol: "AZN", Name: "Azerbaijan Manat", MinorUnit: 2},
	BAM: {Code: BAM, NumericCode: "977", Symbol: "BAM", Name: "Convertible Mark", MinorUnit: 2}, BBD: {Code: BBD, NumericCode: "052", Symbol: "BBD", Name: "Barbados Dollar", MinorUnit: 2},
	BDT: {Code: BDT, NumericCode: "050", Symbol: "BDT", Name: "Taka", MinorUnit: 2}, BGN: {Code: BGN, NumericCode: "975", Symbol: "BGN", Name: "Bulgarian Lev", MinorUnit: 2},
	BHD: {Code: BHD, NumericCode: "048", Symbol: "BHD", Name: "Bahraini Dinar", MinorUnit: 3}, BIF: {Code: BIF, NumericCode: "108", Symbol: "BIF", Name: "Burundi Franc", MinorUnit: 0},
	BMD: {Code: BMD, NumericCode: "060", Symbol: "BMD", Name: "Bermudian Dollar", MinorUnit: 2}, BND: {Code: BND, NumericCode: "096", Symbol: "BND", Name: "Brunei Dollar", MinorUnit: 2},
	BOB: {Code: BOB, NumericCode: "068", Symbol: "BOB", Name: "Boliviano", MinorUnit: 2}, BOV: {Code: BOV, NumericCode: "984", Symbol: "BOV", Name: "Mvdol", MinorUnit: 2},
	BRL: {Code: BRL, NumericCode: "986", Symbol: "BRL", Name: "Brazilian Real", MinorUnit: 2}, BSD: {Code: BSD, NumericCode: "044", Symbol: "BSD", Name: "Bahamian Dollar", MinorUnit: 2},
	BTN: {Code: BTN, NumericCode: "064", Symbol: "BTN", Name: "Ngultrum", MinorUnit: 2}, BWP: {Code: BWP, NumericCode: "072", Symbol: "BWP", Name: "Pula", MinorUnit: 2},
	BYN: {Code: BYN, NumericCode: "933", Symbol: "BYN", Name: "Belarusian Ruble", MinorUnit: 2}, BZD: {Code: BZD, NumericCode: "084", Symbol: "BZD", Name: "Belize Dollar", MinorUnit: 2},
	CAD: {Code: CAD, NumericCode: "124", Symbol: "CAD", Name: "Canadian Dollar", MinorUnit: 2}, CDF: {Code: CDF, NumericCode: "976", Symbol: "CDF", Name: "Congolese Franc", MinorUnit: 2},
	CHE: {Code: CHE, NumericCode: "947", Symbol: "CHE", Name: "WIR Euro", MinorUnit: 2}, CHF: {Code: CHF, NumericCode: "756", Symbol: "CHF", Name: "Swiss Franc", MinorUnit: 2},
	CHW: {Code: CHW, NumericCode: "948", Symbol: "CHW", Name: "WIR Franc", MinorUnit: 2}, CLF: {Code: CLF, NumericCode: "990", Symbol: "CLF", Name: "Unidad de Fomento", MinorUnit: 4},
	CLP: {Code: CLP, NumericCode: "152", Symbol: "CLP", Name: "Chilean Peso", MinorUnit: 0}, CNY: {Code: CNY, NumericCode: "156", Symbol: "CNY", Name: "Yuan Renminbi", MinorUnit: 2},
	COP: {Code: COP, NumericCode: "170", Symbol: "COP", Name: "Colombian Peso", MinorUnit: 2}, COU: {Code: COU, NumericCode: "970", Symbol: "COU", Name: "Unidad de Valor Real", MinorUnit: 2},
	CRC: {Code: CRC, NumericCode: "188", Symbol: "CRC", Name: "Costa Rican Colon", MinorUnit: 2}, CUC: {Code: CUC, NumericCode: "931", Symbol: "CUC", Name: "Peso Convertible", MinorUnit: 2},
	CUP: {Code: CUP, NumericCode: "192", Symbol: "CUP", Name: "Cuban Peso", MinorUnit: 2}, CVE: {Code: CVE, NumericCode: "132", Symbol: "CVE", Name: "Cabo Verde Escudo", MinorUnit: 2},
	CZK: {Code: CZK, NumericCode: "203", Symbol: "CZK", Name: "Czech Koruna", MinorUnit: 2}, DJF: {Code: DJF, NumericCode: "262", Symbol: "DJF", Name: "Djibouti Franc", MinorUnit: 0},
	DKK: {Code: DKK, NumericCode: "208", Symbol: "DKK", Name: "Danish Krone", MinorUnit: 2}, DOP: {Code: DOP, NumericCode: "214", Symbol: "DOP", Name: "Dominican Peso", MinorUnit: 2},
	DZD: {Code: DZD, NumericCode: "012", Symbol: "DZD", Name: "Algerian Dinar", MinorUnit: 2}, EGP: {Code: EGP, NumericCode: "818", Symbol: "EGP", Name: "Egyptian Pound", MinorUnit: 2},
	ERN: {Code: ERN, NumericCode: "232", Symbol: "ERN", Name: "Nakfa", MinorUnit: 2}, ETB: {Code: ETB, NumericCode: "230", Symbol: "ETB", Name: "Ethiopian Birr", MinorUnit: 2},
	EUR: {Code: EUR, NumericCode: "978", Symbol: "EUR", Name: "Euro", MinorUnit: 2}, FJD: {Code: FJD, NumericCode: "242", Symbol: "FJD", Name: "Fiji Dollar", MinorUnit: 2},
	FKP: {Code: FKP, NumericCode: "238", Symbol: "FKP", Name: "Falkland Islands Pound", MinorUnit: 2}, GBP: {Code: GBP, NumericCode: "826", Symbol: "GBP", Name: "Pound Sterling", MinorUnit: 2},
	GEL: {Code: GEL, NumericCode: "981", Symbol: "GEL", Name: "Lari", MinorUnit: 2}, GHS: {Code: GHS, NumericCode: "936", Symbol: "GHS", Name: "Ghana Cedi", MinorUnit: 2},
	GIP: {Code: GIP, NumericCode: "292", Symbol: "GIP", Name: "Gibraltar Pound", MinorUnit: 2}, GMD: {Code: GMD, NumericCode: "270", Symbol: "GMD", Name: "Dalasi", MinorUnit: 2},
	GNF: {Code: GNF, NumericCode: "324", Symbol: "GNF", Name: "Guinean Franc", MinorUnit: 0}, GTQ: {Code: GTQ, NumericCode: "320", Symbol: "GTQ", Name: "Quetzal", MinorUnit: 2},
	GYD: {Code: GYD, NumericCode: "328", Symbol: "GYD", Name: "Guyana Dollar", MinorUnit: 2}, HKD: {Code: HKD, NumericCode: "344", Symbol: "HKD", Name: "Hong Kong Dollar", MinorUnit: 2},
	HNL: {Code: HNL, NumericCode: "340", Symbol: "HNL", Name: "Lempira", MinorUnit: 2}, HTG: {Code: HTG, NumericCode: "332", Symbol: "HTG", Name: "Gourde", MinorUnit: 2},
	HUF: {Code: HUF, NumericCode: "348", Symbol: "HUF", Name: "Forint", MinorUnit: 2}, IDR: {Code: IDR, NumericCode: "360", Symbol: "IDR", Name: "Rupiah", MinorUnit: 2},
	ILS: {Code: ILS, NumericCode: "376", Symbol: "ILS", Name: "New Israeli Sheqel", MinorUnit: 2}, INR: {Code: INR, NumericCode: "356", Symbol: "INR", Name: "Indian Rupee", MinorUnit: 2},
	IQD: {Code: IQD, NumericCode: "368", Symbol: "IQD", Name: "Iraqi Dinar", MinorUnit: 3}, IRR: {Code: IRR, NumericCode: "364", Symbol: "IRR", Name: "Iranian Rial", MinorUnit: 2},
	ISK: {Code: ISK, NumericCode: "352", Symbol: "ISK", Name: "Iceland Krona", MinorUnit: 0}, JMD: {Code: JMD, NumericCode: "388", Symbol: "JMD", Name: "Jamaican Dollar", MinorUnit: 2},
	JOD: {Code: JOD, NumericCode: "400", Symbol: "JOD", Name: "Jordanian Dinar", MinorUnit: 3}, JPY: {Code: JPY, NumericCode: "392", Symbol: "JPY", Name: "Yen", MinorUnit: 0},
	KES: {Code: KES, NumericCode: "404", Symbol: "KES", Name: "Kenyan Shilling", MinorUnit: 2}, KGS: {Code: KGS, NumericCode: "417", Symbol: "KGS", Name: "Som", MinorUnit: 2},
	KHR: {Code: KHR, NumericCode: "116", Symbol: "KHR", Name: "Riel", MinorUnit: 2}, KMF: {Code: KMF, NumericCode: "174", Symbol: "KMF", Name: "Comorian Franc", MinorUnit: 0},
	KPW: {Code: KPW, NumericCode: "408", Symbol: "KPW", Name: "North Korean Won", MinorUnit: 2}, KRW: {Code: KRW, NumericCode: "410", Symbol: "KRW", Name: "Won", MinorUnit: 0},
	KWD: {Code: KWD, NumericCode: "414", Symbol: "KWD", Name: "Kuwaiti Dinar", MinorUnit: 3}, KYD: {Code: KYD, NumericCode: "136", Symbol: "KYD", Name: "Cayman Islands Dollar", MinorUnit: 2},
	KZT: {Code: KZT, NumericCode: "398", Symbol: "KZT", Name: "Tenge", MinorUnit: 2}, LAK: {Code: LAK, NumericCode: "418", Symbol: "LAK", Name: "Lao Kip", MinorUnit: 2},
	LBP: {Code: LBP, NumericCode: "422", Symbol: "LBP", Name: "Lebanese Pound", MinorUnit: 2}, LKR: {Code: LKR, NumericCode: "144", Symbol: "LKR", Name: "Sri Lanka Rupee", MinorUnit: 2},
	LRD: {Code: LRD, NumericCode: "430", Symbol: "LRD", Name: "Liberian Dollar", MinorUnit: 2}, LSL: {Code: LSL, NumericCode: "426", Symbol: "LSL", Name: "Loti", MinorUnit: 2},
	LYD: {Code: LYD, NumericCode: "434", Symbol: "LYD", Name: "Libyan Dinar", MinorUnit: 3}, MAD: {Code: MAD, NumericCode: "504", Symbol: "MAD", Name: "Moroccan Dirham", MinorUnit: 2},
	MDL: {Code: MDL, NumericCode: "498", Symbol: "MDL", Name: "Moldovan Leu", MinorUnit: 2}, MGA: {Code: MGA, NumericCode: "969", Symbol: "MGA", Name: "Malagasy Ariary", MinorUnit: 2},
	MKD: {Code: MKD, NumericCode: "807", Symbol: "MKD", Name: "Denar", MinorUnit: 2}, MMK: {Code: MMK, NumericCode: "104", Symbol: "MMK", Name: "Kyat", MinorUnit: 2},
	MNT: {Code: MNT, NumericCode: "496", Symbol: "MNT", Name: "Tugrik", MinorUnit: 2}, MOP: {Code: MOP, NumericCode: "446", Symbol: "MOP", Name: "Pataca", MinorUnit: 2},
	MRU: {Code: MRU, NumericCode: "929", Symbol: "MRU", Name: "Ouguiya", MinorUnit: 2}, MUR: {Code: MUR, NumericCode: "480", Symbol: "MUR", Name: "Mauritius Rupee", MinorUnit: 2},
	MVR: {Code: MVR, NumericCode: "462", Symbol: "MVR", Name: "Rufiyaa", MinorUnit: 2}, MWK: {Code: MWK, NumericCode: "454", Symbol: "MWK", Name: "Malawi Kwacha", MinorUnit: 2},
	MXN: {Code: MXN, NumericCode: "484", Symbol: "MXN", Name: "Mexican Peso", MinorUnit: 2}, MXV: {Code: MXV, NumericCode: "979", Symbol: "MXV", Name: "Mexican Unidad de Inversion", MinorUnit: 2},
	MYR: {Code: MYR, NumericCode: "458", Symbol: "MYR", Name: "Malaysian Ringgit", MinorUnit: 2}, MZN: {Code: MZN, NumericCode: "943", Symbol: "MZN", Name: "Mozambique Metical", MinorUnit: 2},
	NAD: {Code: NAD, NumericCode: "516", Symbol: "NAD", Name: "Namibia Dollar", MinorUnit: 2}, NGN: {Code: NGN, NumericCode: "566", Symbol: "NGN", Name: "Naira", MinorUnit: 2},
	NIO: {Code: NIO, NumericCode: "558", Symbol: "NIO", Name: "Cordoba Oro", MinorUnit: 2}, NOK: {Code: NOK, NumericCode: "578", Symbol: "NOK", Name: "Norwegian Krone", MinorUnit: 2},
	NPR: {Code: NPR, NumericCode: "524", Symbol: "NPR", Name: "Nepalese Rupee", MinorUnit: 2}, NZD: {Code: NZD, NumericCode: "554", Symbol: "NZD", Name: "New Zealand Dollar", MinorUnit: 2},
	OMR: {Code: OMR, NumericCode: "512", Symbol: "OMR", Name: "Rial Omani", MinorUnit: 3}, PAB: {Code: PAB, NumericCode: "590", Symbol: "PAB", Name: "Balboa", MinorUnit: 2},
	PEN: {Code: PEN, NumericCode: "604", Symbol: "PEN", Name: "Sol", MinorUnit: 2}, PGK: {Code: PGK, NumericCode: "598", Symbol: "PGK", Name: "Kina", MinorUnit: 2},
	PHP: {Code: PHP, NumericCode: "608", Symbol: "PHP", Name: "Philippine Peso", MinorUnit: 2}, PKR: {Code: PKR, NumericCode: "586", Symbol: "PKR", Name: "Pakistan Rupee", MinorUnit: 2},
	PLN: {Code: PLN, NumericCode: "985", Symbol: "PLN", Name: "Zloty", MinorUnit: 2}, PYG: {Code: PYG, NumericCode: "600", Symbol: "PYG", Name: "Guarani", MinorUnit: 0},
	QAR: {Code: QAR, NumericCode: "634", Symbol: "QAR", Name: "Qatari Rial", MinorUnit: 2}, RON: {Code: RON, NumericCode: "946", Symbol: "RON", Name: "Romanian Leu", MinorUnit: 2},
	RSD: {Code: RSD, NumericCode: "941", Symbol: "RSD", Name: "Serbian Dinar", MinorUnit: 2}, RUB: {Code: RUB, NumericCode: "643", Symbol: "RUB", Name: "Russian Ruble", MinorUnit: 2},
	RWF: {Code: RWF, NumericCode: "646", Symbol: "RWF", Name: "Rwanda Franc", MinorUnit: 0}, SAR: {Code: SAR, NumericCode: "682", Symbol: "SAR", Name: "Saudi Riyal", MinorUnit: 2},
	SBD: {Code: SBD, NumericCode: "090", Symbol: "SBD", Name: "Solomon Islands Dollar", MinorUnit: 2}, SCR: {Code: SCR, NumericCode: "690", Symbol: "SCR", Name: "Seychelles Rupee", MinorUnit: 2},
	SDG: {Code: SDG, NumericCode: "938", Symbol: "SDG", Name: "Sudanese Pound", MinorUnit: 2}, SEK: {Code: SEK, NumericCode: "752", Symbol: "SEK", Name: "Swedish Krona", MinorUnit: 2},
	SGD: {Code: SGD, NumericCode: "702", Symbol: "SGD", Name: "Singapore Dollar", MinorUnit: 2}, SHP: {Code: SHP, NumericCode: "654", Symbol: "SHP", Name: "Saint Helena Pound", MinorUnit: 2},
	SLE: {Code: SLE, NumericCode: "925", Symbol: "SLE", Name: "Leone", MinorUnit: 2}, SLL: {Code: SLL, NumericCode: "694", Symbol: "SLL", Name: "Leone", MinorUnit: 2},
	SOS: {Code: SOS, NumericCode: "706", Symbol: "SOS", Name: "Somali Shilling", MinorUnit: 2}, SRD: {Code: SRD, NumericCode: "968", Symbol: "SRD", Name: "Surinam Dollar", MinorUnit: 2},
	SSP: {Code: SSP, NumericCode: "728", Symbol: "SSP", Name: "South Sudanese Pound", MinorUnit: 2}, STN: {Code: STN, NumericCode: "930", Symbol: "STN", Name: "Dobra", MinorUnit: 2},
	SVC: {Code: SVC, NumericCode: "222", Symbol: "SVC", Name: "El Salvador Colon", MinorUnit: 2}, SYP: {Code: SYP, NumericCode: "760", Symbol: "SYP", Name: "Syrian Pound", MinorUnit: 2},
	SZL: {Code: SZL, NumericCode: "748", Symbol: "SZL", Name: "Lilangeni", MinorUnit: 2}, THB: {Code: THB, NumericCode: "764", Symbol: "THB", Name: "Baht", MinorUnit: 2},
	TJS: {Code: TJS, NumericCode: "972", Symbol: "TJS", Name: "Somoni", MinorUnit: 2}, TMT: {Code: TMT, NumericCode: "934", Symbol: "TMT", Name: "Turkmenistan New Manat", MinorUnit: 2},
	TND: {Code: TND, NumericCode: "788", Symbol: "TND", Name: "Tunisian Dinar", MinorUnit: 3}, TOP: {Code: TOP, NumericCode: "776", Symbol: "TOP", Name: "Pa'anga", MinorUnit: 2},
	TRY: {Code: TRY, NumericCode: "949", Symbol: "TRY", Name: "Turkish Lira", MinorUnit: 2}, TTD: {Code: TTD, NumericCode: "780", Symbol: "TTD", Name: "Trinidad and Tobago Dollar", MinorUnit: 2},
	TWD: {Code: TWD, NumericCode: "901", Symbol: "TWD", Name: "New Taiwan Dollar", MinorUnit: 2}, TZS: {Code: TZS, NumericCode: "834", Symbol: "TZS", Name: "Tanzanian Shilling", MinorUnit: 2},
	UAH: {Code: UAH, NumericCode: "980", Symbol: "UAH", Name: "Hryvnia", MinorUnit: 2}, UGX: {Code: UGX, NumericCode: "800", Symbol: "UGX", Name: "Uganda Shilling", MinorUnit: 0},
	USD: {Code: USD, NumericCode: "840", Symbol: "USD", Name: "US Dollar", MinorUnit: 2}, USN: {Code: USN, NumericCode: "997", Symbol: "USN", Name: "US Dollar (Next day)", MinorUnit: 2},
	UYI: {Code: UYI, NumericCode: "940", Symbol: "UYI", Name: "Uruguay Peso en Unidades Indexadas", MinorUnit: 0}, UYU: {Code: UYU, NumericCode: "858", Symbol: "UYU", Name: "Peso Uruguayo", MinorUnit: 2},
	UYW: {Code: UYW, NumericCode: "927", Symbol: "UYW", Name: "Unidad Previsional", MinorUnit: 4}, UZS: {Code: UZS, NumericCode: "860", Symbol: "UZS", Name: "Uzbekistan Sum", MinorUnit: 2},
	VED: {Code: VED, NumericCode: "926", Symbol: "VED", Name: "Bolivar Soberano", MinorUnit: 2}, VES: {Code: VES, NumericCode: "928", Symbol: "VES", Name: "Bolivar Soberano", MinorUnit: 2},
	VND: {Code: VND, NumericCode: "704", Symbol: "VND", Name: "Dong", MinorUnit: 0}, VUV: {Code: VUV, NumericCode: "548", Symbol: "VUV", Name: "Vatu", MinorUnit: 0},
	WST: {Code: WST, NumericCode: "882", Symbol: "WST", Name: "Tala", MinorUnit: 2}, XAF: {Code: XAF, NumericCode: "950", Symbol: "XAF", Name: "CFA Franc BEAC", MinorUnit: 0},
	XCD: {Code: XCD, NumericCode: "951", Symbol: "XCD", Name: "East Caribbean Dollar", MinorUnit: 2}, XDR: {Code: XDR, NumericCode: "960", Symbol: "XDR", Name: "SDR", MinorUnit: 0},
	XOF: {Code: XOF, NumericCode: "952", Symbol: "XOF", Name: "CFA Franc BCEAO", MinorUnit: 0}, XPF: {Code: XPF, NumericCode: "953", Symbol: "XPF", Name: "CFP Franc", MinorUnit: 0},
	YER: {Code: YER, NumericCode: "886", Symbol: "YER", Name: "Yemeni Rial", MinorUnit: 2}, ZAR: {Code: ZAR, NumericCode: "710", Symbol: "ZAR", Name: "Rand", MinorUnit: 2},
	ZMW: {Code: ZMW, NumericCode: "967", Symbol: "ZMW", Name: "Zambian Kwacha", MinorUnit: 2}, ZWG: {Code: ZWG, NumericCode: "924", Symbol: "ZWG", Name: "Zimbabwe Gold", MinorUnit: 2},
	XXX: {Code: XXX, NumericCode: "999", Symbol: "", Name: "No currency", MinorUnit: 0},
}

func LookupCurrencyByNumericCode(numericCode string) (Currency, bool) {
	trimmed := strings.TrimSpace(numericCode)
	for _, currency := range currencies {
		if currency.NumericCode == trimmed {
			return currency, true
		}
	}
	return Currency{}, false
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
