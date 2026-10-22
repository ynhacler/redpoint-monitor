// SIM 电话卡目录（设计 47.2，修订第 189 条）：常见国家 / 地区的运营商与虚拟运营商品牌，带区号与当地币种。
// 选择后号码的区号与价格币种按国家预填。键为 sim-<国家>-<品牌>，保存在订阅的 brand 中，修改名称不影响已保存的订阅。
// 图标：Simple Icons 收录的（Verizon、O2、Vodafone、Rakuten、Airtel）用标志，其余为色块加图标字。
import type { CatalogItem } from './subscriptions'

/** 一个国家 / 地区 */
export interface SimCountry {
  key: string
  name: string
  flag: string
  dial: string
  currency: string
}

/** 国家 / 地区（按目录中的顺序显示） */
export const simCountries: SimCountry[] = [
  { key: 'us', name: '美国', flag: '🇺🇸', dial: '+1', currency: 'USD' },
  { key: 'cn', name: '中国大陆', flag: '🇨🇳', dial: '+86', currency: 'CNY' },
  { key: 'hk', name: '香港', flag: '🇭🇰', dial: '+852', currency: 'HKD' },
  { key: 'mo', name: '澳门', flag: '🇲🇴', dial: '+853', currency: 'MOP' },
  { key: 'gb', name: '英国', flag: '🇬🇧', dial: '+44', currency: 'GBP' },
  { key: 'jp', name: '日本', flag: '🇯🇵', dial: '+81', currency: 'JPY' },
  { key: 'ph', name: '菲律宾', flag: '🇵🇭', dial: '+63', currency: 'PHP' },
  { key: 'za', name: '南非', flag: '🇿🇦', dial: '+27', currency: 'ZAR' },
  { key: 'ng', name: '尼日利亚', flag: '🇳🇬', dial: '+234', currency: 'NGN' },
  { key: 'gh', name: '加纳', flag: '🇬🇭', dial: '+233', currency: 'GHS' },
  { key: 'sg', name: '新加坡', flag: '🇸🇬', dial: '+65', currency: 'SGD' },
  { key: 'tw', name: '台湾', flag: '🇹🇼', dial: '+886', currency: 'TWD' },
  { key: 'th', name: '泰国', flag: '🇹🇭', dial: '+66', currency: 'THB' },
  { key: 'my', name: '马来西亚', flag: '🇲🇾', dial: '+60', currency: 'MYR' },
  { key: 'au', name: '澳大利亚', flag: '🇦🇺', dial: '+61', currency: 'AUD' },
]

// [品牌键, 名称, 颜色?, 图标字?, 标志键?]；第一组为运营商，第二组为虚拟运营商与子品牌
type Entry = [string, string, string?, string?, string?]
const carriers: Record<string, [Entry[], Entry[]]> = {
  us: [
    [['tmobile', 'T-Mobile', '#E20074', 'T'], ['att', 'AT&T', '#00A8E0', 'AT'], ['verizon', 'Verizon', '#CD040B', undefined, 'verizon']],
    [['tello', 'Tello', '#2A2F7A'], ['usmobile', 'US Mobile', '#1F57FF', 'US'], ['mint', 'Mint Mobile', '#3F9C35'], ['visible', 'Visible', '#1800FF'],
      ['googlefi', 'Google Fi', '#4285F4', 'Fi'], ['cricket', 'Cricket', '#58A618'], ['ultra', 'Ultra Mobile', '#F26722'],
      ['redpocket', 'Red Pocket', '#D71920', 'RP'], ['boost', 'Boost Mobile', '#F7941D'], ['h2o', 'H2O Wireless', '#0072BC', 'H₂O']],
  ],
  cn: [
    [['cmcc', '中国移动', '#0085D0', '移'], ['unicom', '中国联通', '#E60027', '联'], ['telecom', '中国电信', '#0072BC', '电'], ['cbn', '中国广电', '#003F98', '广']],
    [['gotone', '全球通', '#0085D0', '全'], ['szx', '神州行', '#0085D0', '神'], ['mzone', '动感地带', '#0085D0', '动'], ['wangka', '腾讯王卡', '#E60027', '王'],
      ['mifen', '米粉卡', '#FF6900', '米'], ['xingka', '电信星卡', '#0072BC', '星'], ['futu', '广电福兔卡', '#003F98', '兔']],
  ],
  hk: [
    [['cmhk', 'CMHK 中国移动香港', '#0085D0', 'CM'], ['3hk', '3HK', '#000000', '3'], ['csl', 'CSL', '#00A0E9'], ['smartone', 'SmarTone', '#E4007F']],
    [['sosim', 'SoSIM', '#E50012', 'So'], ['clubsim', 'Club SIM', '#00A3E0'], ['luckysim', 'Lucky SIM', '#FF6600'], ['mysim', 'MySIM', '#0098DA', 'My'],
      ['wanzhong', '万众', '#C8102E', '万'], ['yaliaojia', '鸭聊佳', '#F5A623', '鸭'], ['hello', 'Hello', '#E4007F'], ['birdie', '自由鸟 Birdie', '#00A859', '鸟']],
  ],
  mo: [
    [['ctm', 'CTM 澳门电讯', '#E30613', 'CTM'], ['ctmo', '中国电信澳门', '#0072BC', '电'], ['3mo', '3 Macau', '#000000', '3']],
    [['ctmchills', 'CTM Chills', '#E30613', 'C'], ['ctmprepaid', 'CTM 储值卡', '#E30613', '储'], ['ctmoprepaid', '电信澳门预付卡', '#0072BC', '预'],
      ['3moprepaid', '3 Macau 储值卡', '#000000', '3']],
  ],
  gb: [
    [['ee', 'EE', '#007B85'], ['o2', 'O2', '#0019A5', undefined, 'o2'], ['vodafone', 'Vodafone', '#E60000', undefined, 'vodafone'], ['three', 'Three', '#000000', '3']],
    [['giffgaff', 'giffgaff', '#111111'], ['voxi', 'VOXI', '#E60000'], ['smarty', 'SMARTY', '#000000'], ['lebara', 'Lebara', '#002F87'],
      ['lyca', 'Lycamobile', '#002C77'], ['tesco', 'Tesco Mobile', '#00539F'], ['1p', '1pMobile', '#6CBE45', '1p'], ['idmobile', 'iD Mobile', '#E2001A', 'iD']],
  ],
  jp: [
    [['docomo', 'NTT docomo', '#CC0033', 'd'], ['au', 'au (KDDI)', '#EB5505', 'au'], ['softbank', 'SoftBank', '#A0A0A0'],
      ['rakuten', '乐天移动 Rakuten Mobile', '#BF0000', undefined, 'rakuten']],
    [['povo', 'povo', '#1A1A1A'], ['ahamo', 'ahamo', '#2DB9C9'], ['linemo', 'LINEMO', '#00B900'], ['ymobile', 'Y!mobile', '#FF0033', 'Y!'],
      ['uqmobile', 'UQ mobile', '#E5007E', 'UQ'], ['iijmio', 'IIJmio', '#00418C', 'IIJ'], ['mineo', 'mineo', '#00A0E9'], ['hismobile', 'HIS Mobile', '#0060A9', 'HIS'],
      ['jcsim', 'Japan Communications SIM', '#004DA0', 'JC']],
  ],
  ph: [
    [['globe', 'Globe', '#0056B8'], ['smart', 'Smart', '#00A651'], ['dito', 'DITO', '#E30613']],
    [['tnt', 'TNT', '#F7941D'], ['tm', 'TM', '#0072BC'], ['gomoph', 'GOMO', '#E5007D'], ['smartprepaid', 'Smart Prepaid', '#00A651'],
      ['globeprepaid', 'Globe Prepaid', '#0056B8']],
  ],
  za: [
    [['mtnza', 'MTN', '#FFCB05'], ['vodacom', 'Vodacom', '#E60000'], ['telkom', 'Telkom', '#0079C1'], ['cellc', 'Cell C', '#000000', 'C']],
    [['mtnprepaid', 'MTN Prepaid', '#FFCB05'], ['vodacomprepaid', 'Vodacom Prepaid', '#E60000']],
  ],
  ng: [
    [['mtnng', 'MTN Nigeria', '#FFCB05'], ['airtelng', 'Airtel Nigeria', '#E40000', undefined, 'airtel'], ['glo', 'Glo', '#4FB848'],
      ['9mobile', '9mobile', '#006D3A', '9']],
    [],
  ],
  gh: [[['mtngh', 'MTN Ghana', '#FFCB05'], ['telecel', 'Telecel Ghana', '#E2001A'], ['atgh', 'AT', '#E40000', 'AT']], []],
  sg: [
    [['singtel', 'Singtel', '#E1251B'], ['starhub', 'StarHub', '#00A94F'], ['m1', 'M1', '#F37021', 'M1'], ['simba', 'SIMBA', '#E4002B']],
    [['gomosg', 'GOMO', '#E5007D'], ['giga', 'giga!', '#FFB800'], ['circles', 'Circles.Life', '#000000'], ['eight', 'eight', '#4A2C8F', '8'],
      ['zero1', 'Zero1', '#000000', '01']],
  ],
  tw: [
    [['cht', '中华电信', '#0074C1', '华'], ['twm', '台湾大哥大', '#E8541F', '大'], ['fet', '远传电信', '#E60012', '远']],
    [['chtprepaid', '中华预付卡', '#0074C1', '预'], ['twmprepaid', '台湾大哥大预付卡', '#E8541F', '预'], ['fetprepaid', '远传易付卡', '#E60012', '易']],
  ],
  th: [
    [['ais', 'AIS', '#97C93D'], ['true', 'True', '#E30613'], ['dtac', 'dtac', '#1DADE4']],
    [['one2call', 'AIS One-2-Call', '#97C93D', '12'], ['truemove', 'TrueMove H', '#E30613', 'H'], ['dtachappy', 'dtac Happy', '#1DADE4', 'H']],
  ],
  my: [
    [['celcomdigi', 'CelcomDigi', '#0045FF', 'CD'], ['maxis', 'Maxis', '#00A94F'], ['umobile', 'U Mobile', '#FF6600', 'U'], ['yes', 'Yes', '#000000']],
    [['hotlink', 'Hotlink', '#E4002B'], ['tunetalk', 'Tune Talk', '#E4002B'], ['redone', 'redONE', '#E31837'], ['unifi', 'Unifi Mobile', '#F26E21']],
  ],
  au: [
    [['telstra', 'Telstra', '#0099F8'], ['optus', 'Optus', '#008381'], ['vodafoneau', 'Vodafone', '#E60000', undefined, 'vodafone']],
    [['aldi', 'ALDI Mobile', '#00005F'], ['amaysim', 'Amaysim', '#E2187F'], ['boostau', 'Boost Mobile', '#F7941D'], ['belong', 'Belong', '#2E2E2E'],
      ['lebaraau', 'Lebara', '#002F87']],
  ],
}

/** SIM 电话卡目录：按国家 / 地区展开为目录项；各国的主要运营商标为“热门”中只取前几个常用的 */
export const simCatalog: CatalogItem[] = simCountries.flatMap((c) => {
  const [mno, mvno] = carriers[c.key] ?? [[], []]
  return [...mno, ...mvno].map(([id, name, color, letter, icon]) => ({
    key: `sim-${c.key}-${id}`, name, category: 'sim' as const, color: color ?? '', letter, icon, country: c.key, dial: c.dial,
    currency: c.currency, hot: ['sim-us-tello', 'sim-jp-rakuten', 'sim-cn-cmcc', 'sim-gb-giffgaff', 'sim-hk-cmhk'].includes(`sim-${c.key}-${id}`),
  }))
})
