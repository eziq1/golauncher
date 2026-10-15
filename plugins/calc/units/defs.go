package units

// def defines a unit and, through prefixes, its prefixed forms.
type def struct {
	sym    string
	cat    Category
	dim    Dim
	factor string // value of one unit in base units: "1609.344", "1000/3600"
	offset string // absolute temperatures
	dyn    Dyn
	recip  bool
	// chinese marks Chinese market units (市制).
	chinese bool
	// displayOnly: sym is shown but not accepted as input ("3d" is more
	// likely a search for a 3D app than three days).
	displayOnly bool
	syms        []string // other case-sensitive symbols
	names       []string // case-insensitive names and phrases
	zh          []string // Chinese names (their pinyin is added)
	// prefixes are the accepted prefix keys; pnames are combined with the
	// prefix names ("kilo"+"meter"); zhPrefixed are Chinese names of
	// prefixed units by prefix key.
	prefixes []string
	// strictPrefixes skips the other spellings of prefixes ("kb" is not a
	// kilobit: case-insensitive "kb" means kilobyte).
	strictPrefixes bool
	pnames         []string
	zhPrefixed     map[string][]string
}

// Dimensions.
var (
	dLength      = Of(Length, 1)
	dArea        = Of(Length, 2)
	dVolume      = Of(Length, 3)
	dMass        = Of(Mass, 1)
	dTime        = Of(Time, 1)
	dTemperature = Of(Temperature, 1)
	dData        = Of(Data, 1)
	dDataRate    = Of(Data, 1).Div(dTime)
	dSpeed       = dLength.Div(dTime)
	dCurrency    = Of(Currency, 1)
	dForce       = dMass.Mul(dLength).Div(dTime.Pow(2))
	dPressure    = dForce.Div(dArea)
	dEnergy      = dForce.Mul(dLength)
	dPower       = dEnergy.Div(dTime)
	dAngle       = Of(Angle, 1)
	dFrequency   = dTime.Neg()
	dFuel        = dVolume.Div(dLength) // L/100km: an area
	dEconomy     = dLength.Div(dVolume) // mpg
	dDensity     = dMass.Div(dVolume)
)

// dimCategories gives the category of a dimension that does not come from
// a single unit, e.g. kg·m/s² is a force. Earlier entries win.
var dimCategories = []struct {
	dim Dim
	cat Category
}{
	{dLength, CatLength}, {dArea, CatArea}, {dVolume, CatVolume}, {dMass, CatMass},
	{dTemperature, CatTemperature}, {dData, CatData}, {dDataRate, CatDataRate},
	{dSpeed, CatSpeed}, {dTime, CatDuration}, {dCurrency, CatCurrency},
	{dPressure, CatPressure}, {dEnergy, CatEnergy}, {dPower, CatPower},
	{dAngle, CatAngle}, {dFrequency, CatFrequency}, {dEconomy, CatFuel},
	{dForce, CatForce}, {dDensity, CatDensity},
}

// FuelDim and TorqueDim are the dimensions of categories that share them
// with others (fuel consumption is an area, torque an energy).
var (
	FuelDim   = dFuel
	TorqueDim = dEnergy
)

// listing is the suggested conversions of a category.
type listing struct {
	metric, imperial, zh []string
	// magnitude picks the units with the most readable numbers.
	magnitude bool
}

var listings = map[Category]listing{
	CatLength: {metric: []string{"m", "km", "cm", "mm", "mi", "ft", "in", "yd", "nmi"},
		imperial: []string{"ft", "in", "mi", "yd", "m", "km", "cm", "mm", "nmi"}, zh: []string{"尺", "寸", "丈", "里"}},
	CatArea: {metric: []string{"m²", "km²", "ha", "ft²", "acre", "cm²", "mi²"},
		imperial: []string{"ft²", "acre", "mi²", "in²", "m²", "ha", "km²"}, zh: []string{"亩", "分", "顷"}},
	CatVolume: {metric: []string{"L", "mL", "m³", "gal", "fl oz", "imp gal", "qt", "pint", "ft³"},
		imperial: []string{"gal", "fl oz", "qt", "pint", "imp gal", "L", "mL", "m³", "ft³"}, zh: []string{"斗"}},
	CatMass: {metric: []string{"kg", "g", "t", "lb", "oz", "mg", "st", "ct"},
		imperial: []string{"lb", "oz", "st", "kg", "g", "t", "ct"}, zh: []string{"斤", "两", "钱"}},
	CatTemperature: {metric: []string{"℃", "℉", "K"}, imperial: []string{"℉", "℃", "K"}},
	CatData: {metric: []string{"b", "B", "KB", "KiB", "MB", "MiB", "GB", "GiB", "TB", "TiB", "PB", "PiB", "Kb", "Mb", "Gb"},
		magnitude: true},
	CatDataRate: {metric: []string{"bps", "kbps", "Mbps", "Gbps", "B/s", "KB/s", "MB/s", "MiB/s", "GB/s"},
		magnitude: true},
	CatSpeed: {metric: []string{"km/h", "m/s", "mph", "kn", "ft/s"},
		imperial: []string{"mph", "ft/s", "km/h", "m/s", "kn"}},
	CatDuration: {metric: []string{"ns", "µs", "ms", "s", "min", "h", "d", "wk", "mo", "yr"}, magnitude: true},
	CatPressure: {metric: []string{"kPa", "bar", "psi", "atm", "mmHg", "hPa", "MPa", "Pa", "inHg", "Torr"},
		imperial: []string{"psi", "inHg", "atm", "bar", "kPa", "mmHg", "hPa", "MPa", "Pa", "Torr"}},
	CatEnergy: {metric: []string{"kJ", "kcal", "kWh", "J", "cal", "BTU", "eV"},
		imperial: []string{"BTU", "kcal", "kWh", "kJ", "J", "cal", "eV"}},
	CatPower: {metric: []string{"kW", "W", "hp", "PS", "BTU/h", "MW"},
		imperial: []string{"hp", "BTU/h", "W", "kW", "PS", "MW"}},
	CatAngle:     {metric: []string{"°", "rad", "grad", "turn", "arcmin", "arcsec"}},
	CatFrequency: {metric: []string{"Hz", "kHz", "MHz", "GHz", "rpm"}},
	CatFuel: {metric: []string{"L/100km", "km/L", "mpg", "mpg (UK)"},
		imperial: []string{"mpg", "mpg (UK)", "L/100km", "km/L"}},
	CatForce:   {metric: []string{"N", "kN", "kgf", "lbf", "dyn"}, imperial: []string{"lbf", "N", "kN", "kgf", "dyn"}},
	CatTorque:  {metric: []string{"N·m", "lbf·ft", "kgf·m"}, imperial: []string{"lbf·ft", "N·m", "kgf·m"}},
	CatDensity: {metric: []string{"kg/m³", "g/cm³", "kg/L", "lb/ft³"}, imperial: []string{"lb/ft³", "kg/m³", "g/cm³", "kg/L"}},
	CatTypography: {metric: []string{"px", "pt", "rem", "em", "dp", "mm", "in"},
		imperial: []string{"px", "pt", "rem", "em", "dp", "in", "mm"}},
	CatCooking: {metric: []string{"mL", "cup", "tbsp", "tsp", "fl oz", "metric cup", "metric tbsp", "metric tsp", "L"},
		imperial: []string{"cup", "tbsp", "tsp", "fl oz", "mL", "metric cup", "L"}},
}

// defs are the unit definitions, most common first: earlier definitions win
// alias conflicts.
func defs() []def {
	var d []def
	// Length.
	d = append(d,
		def{sym: "m", cat: CatLength, dim: dLength, factor: "1", names: []string{"meter", "meters", "metre", "metres"},
			zh: []string{"米", "公尺"}, prefixes: []string{"k", "c", "m", "µ", "n", "d"},
			pnames:     []string{"meter", "meters", "metre", "metres"},
			zhPrefixed: map[string][]string{"k": {"千米", "公里"}, "c": {"厘米", "公分"}, "m": {"毫米"}, "µ": {"微米"}, "n": {"纳米"}, "d": {"分米"}}},
		def{sym: "in", cat: CatLength, dim: dLength, factor: "0.0254", names: []string{"inch", "inches"}, zh: []string{"英寸"}},
		def{sym: "ft", cat: CatLength, dim: dLength, factor: "0.3048", names: []string{"foot", "feet"}, zh: []string{"英尺"}},
		def{sym: "yd", cat: CatLength, dim: dLength, factor: "0.9144", names: []string{"yard", "yards"}, zh: []string{"码"}},
		def{sym: "mi", cat: CatLength, dim: dLength, factor: "1609.344", names: []string{"mile", "miles"}, zh: []string{"英里"}},
		def{sym: "nmi", cat: CatLength, dim: dLength, factor: "1852",
			names: []string{"nautical mile", "nautical miles"}, zh: []string{"海里"}},
		def{sym: "au", cat: CatLength, dim: dLength, factor: "149597870700", names: []string{"astronomical unit", "astronomical units"}, zh: []string{"天文单位"}},
		def{sym: "ly", cat: CatLength, dim: dLength, factor: "9460730472580800", names: []string{"light year", "light years", "light-year", "light-years", "lightyear", "lightyears"}, zh: []string{"光年"}},
		def{sym: "丈", cat: CatLength, dim: dLength, factor: "10/3", chinese: true, zh: []string{"市丈"}},
		def{sym: "尺", cat: CatLength, dim: dLength, factor: "1/3", chinese: true, zh: []string{"市尺"}},
		def{sym: "寸", cat: CatLength, dim: dLength, factor: "1/30", chinese: true, zh: []string{"市寸"}},
		def{sym: "里", cat: CatLength, dim: dLength, factor: "500", chinese: true, zh: []string{"市里", "华里"}},
	)
	// Typography (lengths that depend on the screen).
	d = append(d,
		def{sym: "px", cat: CatTypography, dim: dLength, dyn: DynPixel, names: []string{"pixel", "pixels"}, zh: []string{"像素"}},
		def{sym: "pt", cat: CatTypography, dim: dLength, factor: "0.0254/72", names: []string{"point", "points"}},
		def{sym: "rem", cat: CatTypography, dim: dLength, dyn: DynRem},
		def{sym: "em", cat: CatTypography, dim: dLength, dyn: DynRem},
		def{sym: "dp", cat: CatTypography, dim: dLength, factor: "0.0254/160", syms: []string{"dip", "sp"}},
	)
	// Area.
	d = append(d,
		def{sym: "m²", cat: CatArea, dim: dArea, factor: "1", syms: []string{"sqm"},
			names: []string{"sq m", "square meter", "square meters", "square metre", "square metres"}, zh: []string{"平方米", "平米"}},
		def{sym: "km²", cat: CatArea, dim: dArea, factor: "1000000", syms: []string{"sqkm"},
			names: []string{"sq km", "square kilometer", "square kilometers", "square kilometre", "square kilometres"}, zh: []string{"平方公里", "平方千米"}},
		def{sym: "cm²", cat: CatArea, dim: dArea, factor: "0.0001",
			names: []string{"sq cm", "square centimeter", "square centimeters", "square centimetre", "square centimetres"}, zh: []string{"平方厘米"}},
		def{sym: "mm²", cat: CatArea, dim: dArea, factor: "0.000001",
			names: []string{"sq mm", "square millimeter", "square millimeters"}, zh: []string{"平方毫米"}},
		def{sym: "ha", cat: CatArea, dim: dArea, factor: "10000", names: []string{"hectare", "hectares"}, zh: []string{"公顷"}},
		def{sym: "acre", cat: CatArea, dim: dArea, factor: "4046.8564224", syms: []string{"ac"}, names: []string{"acres"}, zh: []string{"英亩"}},
		def{sym: "ft²", cat: CatArea, dim: dArea, factor: "0.09290304", syms: []string{"sqft"},
			names: []string{"sq ft", "square foot", "square feet"}, zh: []string{"平方英尺"}},
		def{sym: "in²", cat: CatArea, dim: dArea, factor: "0.00064516", syms: []string{"sqin"},
			names: []string{"sq in", "square inch", "square inches"}, zh: []string{"平方英寸"}},
		def{sym: "yd²", cat: CatArea, dim: dArea, factor: "0.83612736",
			names: []string{"sq yd", "square yard", "square yards"}, zh: []string{"平方码"}},
		def{sym: "mi²", cat: CatArea, dim: dArea, factor: "2589988.110336", syms: []string{"sqmi"},
			names: []string{"sq mi", "square mile", "square miles"}, zh: []string{"平方英里"}},
		def{sym: "亩", cat: CatArea, dim: dArea, factor: "10000/15", chinese: true, zh: []string{"市亩"}},
		def{sym: "分", cat: CatArea, dim: dArea, factor: "1000/15", chinese: true},
		def{sym: "顷", cat: CatArea, dim: dArea, factor: "1000000/15", chinese: true},
	)
	// Volume and cooking.
	d = append(d,
		def{sym: "L", cat: CatVolume, dim: dVolume, factor: "0.001", syms: []string{"l", "ℓ"},
			names: []string{"liter", "liters", "litre", "litres"}, zh: []string{"升", "公升", "市升"},
			prefixes: []string{"m", "c", "d", "µ", "h"}, pnames: []string{"liter", "liters", "litre", "litres"},
			zhPrefixed: map[string][]string{"m": {"毫升"}, "c": {"厘升"}, "d": {"分升"}, "µ": {"微升"}}},
		def{sym: "m³", cat: CatVolume, dim: dVolume, factor: "1",
			names: []string{"cu m", "cubic meter", "cubic meters", "cubic metre", "cubic metres"}, zh: []string{"立方米", "方"}},
		def{sym: "cm³", cat: CatVolume, dim: dVolume, factor: "0.000001", syms: []string{"cc"},
			names: []string{"cu cm", "cubic centimeter", "cubic centimeters"}, zh: []string{"立方厘米"}},
		def{sym: "gal", cat: CatVolume, dim: dVolume, factor: "0.003785411784",
			names: []string{"gallon", "gallons", "us gal", "us gallon", "us gallons"}, zh: []string{"加仑", "美制加仑"}},
		def{sym: "imp gal", cat: CatVolume, dim: dVolume, factor: "0.00454609",
			names: []string{"uk gal", "imperial gallon", "imperial gallons", "uk gallon", "uk gallons"}, zh: []string{"英制加仑"}},
		def{sym: "qt", cat: CatVolume, dim: dVolume, factor: "0.000946352946", names: []string{"quart", "quarts"}, zh: []string{"夸脱"}},
		def{sym: "pint", cat: CatVolume, dim: dVolume, factor: "0.000473176473",
			names: []string{"pints", "us pint", "us pints"}, zh: []string{"品脱"}},
		def{sym: "imp pint", cat: CatVolume, dim: dVolume, factor: "0.00056826125", names: []string{"uk pint", "uk pints", "imperial pint", "imperial pints"}},
		def{sym: "fl oz", cat: CatVolume, dim: dVolume, factor: "0.0000295735295625", syms: []string{"floz"},
			names: []string{"us fl oz", "fluid ounce", "fluid ounces"}, zh: []string{"液量盎司", "液盎司"}},
		def{sym: "imp fl oz", cat: CatVolume, dim: dVolume, factor: "0.0000284130625", names: []string{"uk fl oz"}},
		def{sym: "ft³", cat: CatVolume, dim: dVolume, factor: "0.028316846592",
			names: []string{"cu ft", "cubic foot", "cubic feet"}, zh: []string{"立方英尺"}},
		def{sym: "in³", cat: CatVolume, dim: dVolume, factor: "0.000016387064", names: []string{"cu in", "cubic inch", "cubic inches"}},
		def{sym: "bbl", cat: CatVolume, dim: dVolume, factor: "0.158987294928", names: []string{"barrel", "barrels"}, zh: []string{"桶"}},
		def{sym: "斗", cat: CatVolume, dim: dVolume, factor: "0.01", chinese: true, zh: []string{"市斗"}},
		def{sym: "cup", cat: CatCooking, dim: dVolume, factor: "0.0002365882365", names: []string{"cups", "us cup", "us cups"}, zh: []string{"杯"}},
		def{sym: "tbsp", cat: CatCooking, dim: dVolume, factor: "0.00001478676478125",
			names: []string{"tablespoon", "tablespoons", "us tbsp"}, zh: []string{"汤匙", "大勺"}},
		def{sym: "tsp", cat: CatCooking, dim: dVolume, factor: "0.00000492892159375",
			names: []string{"teaspoon", "teaspoons", "us tsp"}, zh: []string{"茶匙", "小勺"}},
		def{sym: "metric cup", cat: CatCooking, dim: dVolume, factor: "0.00025", names: []string{"metric cups"}, zh: []string{"公制杯"}},
		def{sym: "metric tbsp", cat: CatCooking, dim: dVolume, factor: "0.000015", names: []string{"metric tablespoon", "metric tablespoons"}},
		def{sym: "metric tsp", cat: CatCooking, dim: dVolume, factor: "0.000005", names: []string{"metric teaspoon", "metric teaspoons"}},
	)
	// Mass.
	d = append(d,
		def{sym: "g", cat: CatMass, dim: dMass, factor: "0.001", names: []string{"gram", "grams", "gramme", "grammes"}, zh: []string{"克"},
			prefixes: []string{"k", "m", "µ"}, pnames: []string{"gram", "grams", "gramme", "grammes"},
			zhPrefixed: map[string][]string{"k": {"千克", "公斤"}, "m": {"毫克"}, "µ": {"微克"}}},
		def{sym: "t", cat: CatMass, dim: dMass, factor: "1000", names: []string{"tonne", "tonnes", "metric ton", "metric tons"}, zh: []string{"吨", "公吨"}},
		def{sym: "lb", cat: CatMass, dim: dMass, factor: "0.45359237", syms: []string{"lbs"}, names: []string{"pound", "pounds"}, zh: []string{"磅"}},
		def{sym: "oz", cat: CatMass, dim: dMass, factor: "0.028349523125", names: []string{"ounce", "ounces"}, zh: []string{"盎司"}},
		def{sym: "st", cat: CatMass, dim: dMass, factor: "6.35029318", names: []string{"stone", "stones"}, zh: []string{"英石"}},
		def{sym: "ct", cat: CatMass, dim: dMass, factor: "0.0002", names: []string{"carat", "carats"}, zh: []string{"克拉"}},
		def{sym: "gr", cat: CatMass, dim: dMass, factor: "0.00006479891", names: []string{"grain", "grains"}, zh: []string{"格令"}},
		def{sym: "ozt", cat: CatMass, dim: dMass, factor: "0.0311034768", names: []string{"troy ounce", "troy ounces", "oz t"}, zh: []string{"金衡盎司"}},
		def{sym: "ton", cat: CatMass, dim: dMass, factor: "907.18474", names: []string{"tons", "short ton", "short tons", "us ton", "us tons"}, zh: []string{"短吨"}},
		def{sym: "long ton", cat: CatMass, dim: dMass, factor: "1016.0469088", names: []string{"long tons", "uk ton", "uk tons"}, zh: []string{"长吨"}},
		def{sym: "斤", cat: CatMass, dim: dMass, factor: "0.5", chinese: true, zh: []string{"市斤"}},
		def{sym: "两", cat: CatMass, dim: dMass, factor: "0.05", chinese: true, zh: []string{"市两"}},
		def{sym: "钱", cat: CatMass, dim: dMass, factor: "0.005", chinese: true, zh: []string{"市钱"}},
	)
	// Temperature. Base: kelvin.
	d = append(d,
		def{sym: "℃", cat: CatTemperature, dim: dTemperature, factor: "1", offset: "273.15",
			syms: []string{"°C", "C", "c", "degC"}, names: []string{"celsius", "degree celsius", "degrees celsius", "centigrade"}, zh: []string{"摄氏度"}},
		def{sym: "℉", cat: CatTemperature, dim: dTemperature, factor: "5/9", offset: "45967/180",
			syms: []string{"°F", "F", "f", "degF"}, names: []string{"fahrenheit", "degree fahrenheit", "degrees fahrenheit"}, zh: []string{"华氏度"}},
		def{sym: "K", cat: CatTemperature, dim: dTemperature, factor: "1", offset: "0",
			names: []string{"kelvin", "kelvins"}, zh: []string{"开尔文", "开"}},
	)
	// Data: bytes before bits so that case-insensitive "mb" is a megabyte.
	dataPrefixes := []string{"K", "M", "G", "T", "P", "E", "Ki", "Mi", "Gi", "Ti", "Pi", "Ei"}
	d = append(d,
		def{sym: "B", cat: CatData, dim: dData, factor: "8", names: []string{"byte", "bytes"}, zh: []string{"字节"},
			prefixes: dataPrefixes, pnames: []string{"byte", "bytes"}},
		def{sym: "b", cat: CatData, dim: dData, factor: "1", syms: []string{"bit"}, names: []string{"bits"}, zh: []string{"比特", "位"},
			prefixes: dataPrefixes, strictPrefixes: true, pnames: []string{"bit", "bits"}},
		def{sym: "Kbit", cat: CatData, dim: dData, factor: "1000", syms: []string{"kbit"}},
		def{sym: "Mbit", cat: CatData, dim: dData, factor: "1000000"},
		def{sym: "Gbit", cat: CatData, dim: dData, factor: "1000000000"},
		def{sym: "Tbit", cat: CatData, dim: dData, factor: "1000000000000"},
	)
	// Data rate.
	d = append(d,
		def{sym: "bps", cat: CatDataRate, dim: dDataRate, factor: "1", names: []string{"bits per second"},
			prefixes: []string{"k", "M", "G", "T"}, syms: []string{"bit/s"}},
		def{sym: "B/s", cat: CatDataRate, dim: dDataRate, factor: "8", syms: []string{"Bps"}, names: []string{"bytes per second"}},
		def{sym: "KB/s", cat: CatDataRate, dim: dDataRate, factor: "8000", syms: []string{"kB/s", "KBps", "kBps"}},
		def{sym: "MB/s", cat: CatDataRate, dim: dDataRate, factor: "8000000", syms: []string{"MBps"}},
		def{sym: "GB/s", cat: CatDataRate, dim: dDataRate, factor: "8000000000", syms: []string{"GBps"}},
		def{sym: "MiB/s", cat: CatDataRate, dim: dDataRate, factor: "8388608"},
	)
	// Speed.
	d = append(d,
		def{sym: "km/h", cat: CatSpeed, dim: dSpeed, factor: "1000/3600", syms: []string{"kph", "kmh", "kmph"},
			names: []string{"kilometers per hour", "kilometres per hour"}, zh: []string{"公里每小时", "千米每小时", "公里/小时"}},
		def{sym: "m/s", cat: CatSpeed, dim: dSpeed, factor: "1", syms: []string{"mps"}, names: []string{"meters per second", "metres per second"}, zh: []string{"米每秒"}},
		def{sym: "mph", cat: CatSpeed, dim: dSpeed, factor: "1609.344/3600", names: []string{"miles per hour"}, zh: []string{"英里每小时"}},
		def{sym: "kn", cat: CatSpeed, dim: dSpeed, factor: "1852/3600", syms: []string{"kt"}, names: []string{"knot", "knots"}, zh: []string{"节"}},
		def{sym: "ft/s", cat: CatSpeed, dim: dSpeed, factor: "0.3048", syms: []string{"fps"}, names: []string{"feet per second"}},
	)
	// Duration.
	d = append(d,
		def{sym: "s", cat: CatDuration, dim: dTime, factor: "1", syms: []string{"sec", "secs"}, names: []string{"second", "seconds"}, zh: []string{"秒", "秒钟"},
			prefixes: []string{"m", "µ", "n"}, pnames: []string{"second", "seconds"},
			zhPrefixed: map[string][]string{"m": {"毫秒"}, "µ": {"微秒"}, "n": {"纳秒"}}},
		def{sym: "min", cat: CatDuration, dim: dTime, factor: "60", syms: []string{"mins"}, names: []string{"minute", "minutes"}, zh: []string{"分钟"}},
		def{sym: "h", cat: CatDuration, dim: dTime, factor: "3600", syms: []string{"hr", "hrs"}, names: []string{"hour", "hours"}, zh: []string{"小时", "钟头"}},
		def{sym: "d", displayOnly: true, cat: CatDuration, dim: dTime, factor: "86400", names: []string{"day", "days"}, zh: []string{"天", "日"}},
		def{sym: "wk", cat: CatDuration, dim: dTime, factor: "604800", names: []string{"week", "weeks"}, zh: []string{"周", "星期", "礼拜"}},
		def{sym: "mo", cat: CatDuration, dim: dTime, factor: "2629746", names: []string{"month", "months"}, zh: []string{"月", "个月"}},
		def{sym: "yr", cat: CatDuration, dim: dTime, factor: "31556952", names: []string{"year", "years"}, zh: []string{"年"}},
	)
	// Pressure.
	d = append(d,
		def{sym: "Pa", cat: CatPressure, dim: dPressure, factor: "1", names: []string{"pascal", "pascals"}, zh: []string{"帕", "帕斯卡"},
			prefixes: []string{"h", "k", "M", "G"}, pnames: []string{"pascal", "pascals"},
			zhPrefixed: map[string][]string{"h": {"百帕"}, "k": {"千帕"}, "M": {"兆帕"}}},
		def{sym: "bar", cat: CatPressure, dim: dPressure, factor: "100000", names: []string{"bars"}, zh: []string{"巴"}},
		def{sym: "mbar", cat: CatPressure, dim: dPressure, factor: "100", names: []string{"millibar", "millibars"}, zh: []string{"毫巴"}},
		def{sym: "psi", cat: CatPressure, dim: dPressure, factor: "0.45359237*9.80665/0.00064516", names: []string{"pounds per square inch"}, zh: []string{"磅力每平方英寸"}},
		def{sym: "atm", cat: CatPressure, dim: dPressure, factor: "101325", names: []string{"atmosphere", "atmospheres"}, zh: []string{"标准大气压", "大气压"}},
		def{sym: "mmHg", cat: CatPressure, dim: dPressure, factor: "133.322387415", zh: []string{"毫米汞柱"}},
		def{sym: "inHg", cat: CatPressure, dim: dPressure, factor: "3386.389", zh: []string{"英寸汞柱"}},
		def{sym: "Torr", cat: CatPressure, dim: dPressure, factor: "101325/760", names: []string{"torr"}, zh: []string{"托"}},
	)
	// Energy.
	d = append(d,
		def{sym: "J", cat: CatEnergy, dim: dEnergy, factor: "1", names: []string{"joule", "joules"}, zh: []string{"焦", "焦耳"},
			prefixes: []string{"k", "M", "G"}, pnames: []string{"joule", "joules"},
			zhPrefixed: map[string][]string{"k": {"千焦"}, "M": {"兆焦"}}},
		def{sym: "cal", cat: CatEnergy, dim: dEnergy, factor: "4.184", names: []string{"calorie", "calories"}, zh: []string{"卡", "卡路里"},
			prefixes: []string{"k"}, pnames: []string{"calorie", "calories"}, zhPrefixed: map[string][]string{"k": {"千卡", "大卡"}}},
		def{sym: "Wh", cat: CatEnergy, dim: dEnergy, factor: "3600", names: []string{"watt hour", "watt hours", "watt-hour", "watt-hours"},
			prefixes: []string{"k", "M", "G", "T"}, pnames: []string{"watt hour", "watt hours", "watt-hour", "watt-hours"},
			zhPrefixed: map[string][]string{"k": {"千瓦时", "度电"}}},
		def{sym: "BTU", cat: CatEnergy, dim: dEnergy, factor: "1055.05585262", syms: []string{"Btu"}, names: []string{"british thermal unit", "british thermal units"}, zh: []string{"英热单位"}},
		def{sym: "eV", cat: CatEnergy, dim: dEnergy, factor: "1.602176634e-19", names: []string{"electronvolt", "electronvolts", "electron volt", "electron volts"}, zh: []string{"电子伏特"}},
	)
	// Power.
	d = append(d,
		def{sym: "W", cat: CatPower, dim: dPower, factor: "1", names: []string{"watt", "watts"}, zh: []string{"瓦", "瓦特"},
			prefixes: []string{"m", "k", "M", "G"}, pnames: []string{"watt", "watts"},
			zhPrefixed: map[string][]string{"k": {"千瓦"}, "M": {"兆瓦"}, "m": {"毫瓦"}}},
		def{sym: "hp", cat: CatPower, dim: dPower, factor: "745.69987158227022", names: []string{"horsepower"}, zh: []string{"英制马力"}},
		def{sym: "PS", cat: CatPower, dim: dPower, factor: "735.49875", names: []string{"metric horsepower"}, zh: []string{"马力", "公制马力"}},
		def{sym: "BTU/h", cat: CatPower, dim: dPower, factor: "1055.05585262/3600"},
	)
	// Angle.
	d = append(d,
		def{sym: "°", cat: CatAngle, dim: dAngle, factor: "3.14159265358979323846264338327950288419716939937510582097494459/180",
			syms: []string{"deg"}, names: []string{"degree", "degrees"}, zh: []string{"角度"}},
		def{sym: "rad", cat: CatAngle, dim: dAngle, factor: "1", names: []string{"radian", "radians"}, zh: []string{"弧度"}},
		def{sym: "grad", cat: CatAngle, dim: dAngle, factor: "3.14159265358979323846264338327950288419716939937510582097494459/200",
			syms: []string{"gon"}, names: []string{"gradian", "gradians"}, zh: []string{"百分度"}},
		def{sym: "turn", cat: CatAngle, dim: dAngle, factor: "6.28318530717958647692528676655900576839433879875021164194988918",
			syms: []string{"rev"}, names: []string{"turns", "revolution", "revolutions"}, zh: []string{"圈", "周角"}},
		def{sym: "arcmin", cat: CatAngle, dim: dAngle, factor: "3.14159265358979323846264338327950288419716939937510582097494459/10800",
			syms: []string{"′"}, names: []string{"arcminute", "arcminutes"}, zh: []string{"角分"}},
		def{sym: "arcsec", cat: CatAngle, dim: dAngle, factor: "3.14159265358979323846264338327950288419716939937510582097494459/648000",
			syms: []string{"″"}, names: []string{"arcsecond", "arcseconds"}, zh: []string{"角秒"}},
	)
	// Frequency.
	d = append(d,
		def{sym: "Hz", cat: CatFrequency, dim: dFrequency, factor: "1", names: []string{"hertz"}, zh: []string{"赫兹", "赫"},
			prefixes: []string{"k", "M", "G", "T"}, pnames: []string{"hertz"},
			zhPrefixed: map[string][]string{"k": {"千赫"}, "M": {"兆赫"}, "G": {"吉赫"}}},
		def{sym: "rpm", cat: CatFrequency, dim: dFrequency, factor: "1/60", syms: []string{"RPM"}, names: []string{"revolutions per minute"}, zh: []string{"转每分"}},
		def{sym: "bpm", cat: CatFrequency, dim: dFrequency, factor: "1/60", names: []string{"beats per minute"}},
	)
	// Fuel consumption and economy.
	d = append(d,
		def{sym: "L/100km", cat: CatFuel, dim: dFuel, factor: "0.001/100000", recip: true,
			names: []string{"liters per 100 km", "litres per 100 km"}, zh: []string{"升每百公里", "百公里油耗"}},
		def{sym: "km/L", cat: CatFuel, dim: dEconomy, factor: "1000/0.001", recip: true, syms: []string{"kmpl", "km/l"},
			names: []string{"kilometers per liter", "kilometres per litre"}, zh: []string{"公里每升"}},
		def{sym: "mpg", cat: CatFuel, dim: dEconomy, factor: "1609.344/0.003785411784", recip: true,
			names: []string{"us mpg", "miles per gallon"}},
		def{sym: "mpg (UK)", cat: CatFuel, dim: dEconomy, factor: "1609.344/0.00454609", recip: true,
			syms: []string{"mpgimp", "mpguk"}, names: []string{"uk mpg", "imp mpg", "mpg uk", "mpg imp", "imperial mpg"}},
	)
	// Force, torque, density.
	d = append(d,
		def{sym: "N", cat: CatForce, dim: dForce, factor: "1", names: []string{"newton", "newtons"}, zh: []string{"牛", "牛顿"},
			prefixes: []string{"k", "m"}, pnames: []string{"newton", "newtons"}, zhPrefixed: map[string][]string{"k": {"千牛"}}},
		def{sym: "kgf", cat: CatForce, dim: dForce, factor: "9.80665", names: []string{"kilogram-force", "kilogram force"}, zh: []string{"千克力", "公斤力"}},
		def{sym: "lbf", cat: CatForce, dim: dForce, factor: "0.45359237*9.80665", names: []string{"pound-force", "pound force"}, zh: []string{"磅力"}},
		def{sym: "dyn", cat: CatForce, dim: dForce, factor: "0.00001", names: []string{"dyne", "dynes"}, zh: []string{"达因"}},
		def{sym: "N·m", cat: CatTorque, dim: dEnergy, factor: "1", syms: []string{"Nm", "N*m", "N-m"},
			names: []string{"newton meter", "newton meters", "newton metre", "newton metres"}, zh: []string{"牛米", "牛顿米"}},
		def{sym: "lbf·ft", cat: CatTorque, dim: dEnergy, factor: "0.45359237*9.80665*0.3048",
			syms: []string{"lb-ft", "ft-lb", "lbf-ft", "ft-lbf", "lbft", "ftlb"}, names: []string{"pound-foot", "pound-feet", "foot-pound", "foot-pounds"}},
		def{sym: "kgf·m", cat: CatTorque, dim: dEnergy, factor: "9.80665", syms: []string{"kgfm", "kgf-m"}, zh: []string{"公斤米"}},
		def{sym: "kg/m³", cat: CatDensity, dim: dDensity, factor: "1", zh: []string{"千克每立方米"}},
		def{sym: "g/cm³", cat: CatDensity, dim: dDensity, factor: "1000", syms: []string{"g/cc"}, zh: []string{"克每立方厘米"}},
		def{sym: "kg/L", cat: CatDensity, dim: dDensity, factor: "1000", syms: []string{"g/mL", "g/ml", "kg/l"}},
		def{sym: "lb/ft³", cat: CatDensity, dim: dDensity, factor: "0.45359237/0.028316846592"},
	)
	return append(d, currencyDefs()...)
}
