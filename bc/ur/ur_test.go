package ur

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"math/bits"
	"slices"
	"strings"
	"testing"
)

func TestDecode(t *testing.T) {
	tests := []struct {
		urs      []string
		wantType string
		want     string
		seqLen   int
		seqNums  []int
		error    bool
	}{
		{[]string{"r:crypto-seed/oyadgdiywlamaejszswdwytltifeenftlnmnwkbdhnssro"}, "", "", 0, nil, true},
		{
			[]string{"ur:crypto-seed/oyadgdiywlamaejszswdwytltifeenftlnmnwkbdhnssro"},
			"crypto-seed", "a1015066e9060071faeaeed5d045363a868ef4",
			1, []int{1},
			false,
		},
		{
			[]string{"ur:crypto-output/taadmetaadmtoeadadaolftaaddloxaxhdclaxsbsgptsolkltkndsmskiaelfhhmdimcnmnlgutzotecpsfveylgrbdhptbpsveosaahdcxhnganelacwldjnlschnyfxjyplrllfdrplpswdnbuyctlpwyfmmhgsgtwsrymtldamtaaddyoeadlaaxaeattaaddyoyadlnadwkaewklawktaaddloxaxhdclaoztnnhtwtpslgndfnwpzedrlomnclchrdfsayntlplplojznslfjejecpptlgbgwdaahdcxwtmhnyzmpkkbvdpyvwutglbeahmktyuogusnjonththhdwpsfzvdfpdlcndlkensamtaaddyoeadlfaewkaocyrycmrnvwattaaddyoyadlnaewkaewklawktdbsfttn"},
			"crypto-output", "d90191d90196a201010282d9012fa403582103cbcaa9c98c877a26977d00825c956a238e8dddfbd322cce4f74b0b5bd6ace4a704582060499f801b896d83179a4374aeb7822aaeaceaa0db1f85ee3e904c4defbd968906d90130a20180030007d90130a1018601f400f480f4d9012fa403582102fc9e5af0ac8d9b3cecfe2a888e2117ba3d089d8585886c9c826b6b22a98d12ea045820f0909affaa7ee7abe5dd4e100598d4dc53cd709d5a5c2cac40e7412f232f7c9c06d90130a2018200f4021abd16bee507d90130a1018600f400f480f4",
			1, []int{1},
			false,
		},
		{
			[]string{
				"UR:BYTES/1-3/LPADAXCFAODNCYDLMSDPONHDRHHKAODECNCXFWJZKPIHHGHSJZJZIHJYCXGTKPJZJYINJKINIOCXJKIHJYKPJOCXIYINJZIHBKCNCXJYISINJKCXIYINJZIHCXIAJLJTJYHSINJTJKCXJLJTJZKKCXJOKPIDJZINIACXJEIHKKJKCXHSJTIECXINJKCXJKHSIYIHCXJYJLBKCNCXIEINJKJYJPINIDKPJYIHCXHSJNJLJTIOCXIAJLJKINIOJTIHJPJKBKCNBKGLHSJNIHFTCXJKISBKGDJLJZINIAKKFTCXEYCXJLIYCXEOBKFYIHJPINKOHSJYINJLJTFTCXJNDLEEETDIDLDYDIDLDYDIDLEYDIBKFGJLJPJNHSJYFTCXGDEYHGGUFDBKLSTSKGSS",
				"UR:BYTES/2-3/LPAOAXCFAODNCYDLMSDPONHDRHBKECFPDYETDYEEFEEOFTCXKSJOKPIDENFGEHEEETGSJTIMGOISFLJPFDIYFEGLENGDHSETHFJEKTFGETGSENFGGEJSHKFPGSKSFPJEKPFDIYHSIAIYHFISGTGSHFHKEEGTGMKPGOHFGTKSJPESJOIOKPFPKOENEMFYFDKSEHHKFGKSJSJLGRGLETJKEEGYIYHTJYFYESJKGMEYKSGMFXIYIYGHJSINESFEETFGINFGGSFPHKJEETBKBKFYFYEEFGFPFYFEFEFTCXKSJOKPIDENFYJTIHIEINGOKPHKETGDIAIAENFGIHIMETHKJYEYHTJTJYGDFXKKFGIEJOIDFDFWJEGLHFEMFEHSKTIHJKGMGTIDIAENVSAEGDBD",
				"UR:BYTES/3-3/LPAXAXCFAODNCYDLMSDPONHDRHINESGTGRGRGTISGRFEKOEEGEGTGTKNKTFYGEIAJEHSHFEEIAKNFWKOGLIEIAENINJEKTGSINHTJSIEGOJSGTIEECHTGRGYFLHKHSGYGHEEIAHDGTIHHFIMIYBKBKESFWFPFXFYECFXDYFTCXKSJOKPIDENFEIHIYJPFXJPGTFPIEKPISGLKTJTJKFDIDEOIEFPJKETFYHKHTGUKTEEIYENEOHGKKGMENFYHSFEFWKKGOFDIMKTKOGDFYIEISIAKNIMEHECFGKKFWFWFLEEJYIDFEGEJYIYEEKOGMGRGHKOEHJTIOECGUGDGDJTHGKOEHGDKOIHEHIYEHECFEGEIYINFWHKECJLHKFYGLENHFGSFEFXBKBKHEMEBWOX",
			},
			"bytes", "5902282320426c756557616c6c6574204d756c74697369672073657475702066696c650a2320746869732066696c6520636f6e7461696e73206f6e6c79207075626c6963206b65797320616e64206973207361666520746f0a23206469737472696275746520616d6f6e6720636f7369676e6572730a230a4e616d653a2073680a506f6c6963793a2032206f6620330a44657269766174696f6e3a206d2f3438272f30272f30272f32270a466f726d61743a2050325753480a0a35413038303445333a207870756236463134384c6e6a556847724866454e36506138566b7746384c36464a7159414c78416b75486661636656684d4c5659344d527555564d7872397067754176363744487831594678716f4b4e38733451665a74443973523278524366665471693945384669464c41596b380a0a44443446414445453a207870756236446e656469557559385063633646656a385974325a6e745043794664706248426b4e56374561776573524d62633669394d4b4b4d684b4576344a4d4d7a77444a636b615634637a42764e646336696b774c695a716455714d64355a4b5147596151543463584d65566a660a0a39424143443543303a2078707562364565667243724d416475684e776e734862336441733844595a53773466363357795236446145427955486a777650446468637a6a31354679424247347462454a74663476524b5476316e67355350506e57763150766531663135454a66694259356f59444e36564c45430a0a",
			3, []int{1, 2, 3},
			false,
		},
		{[]string{"r:crypto-seed/oyadgdiywlamaejszswdwytltifeenftlnmnwkbdhnssro"}, "", "", 0, nil, true},
		{
			[]string{
				"UR:CRYPTO-OUTPUT/1347-2/LPCFAHFXAOCFADIOCYCMSWIDBYHDQZCYHNOEDWSBMUAMWYOTAHPFFXNECKNBNTHKDEADHLVLJKLYCMAHTAADEHOEADAEAOAEAMTAADDYOTADLOCSDYYKAEYKAEYKAOYKAOCYUTGWPMWYAXAAAYCYCPMTMUKTTAADDLOLAOWKAXHDCLAOZOJPGDLBSABTUYPTDTMEPAKEGRQZIYBWBKTAFTLOJTJKCHGDEORKFXVLRFKSHTJNAAHDCXMDQDGABWMULBONWNSWCXHPGMHPREKIVYGYKODAVTFELNREMDRNISVDBWIDTEWESKAHTAADEHOEADAEAOAEAMTAADDYOTADLOCSDYYKAEYKAEYKAOYKAOCYNDPSTLRTAXAAAYCYMSWPETYTAEVDTLISPT",
				"UR:CRYPTO-OUTPUT/1355-2/LPCFAHGRAOCFADIOCYCMSWIDBYHDQZSRHSEOYKSGAAOXWSOYATEONYNNEHAMNEPMDNHKKEVTTNROHHDRSRGLPDSRFRJSJEHFTOLGBAHLCFJTMHLUDWTEESVWJPTYPFMOTLHTJPJZPTRPCNURVTCMNLTPNTENGMATUYTBTIHPVEWTVTKKCEJKZOHEPLGHKIYLGSESMDKICLTPCMCMTETPBDDRJLJKBZGDECIDFWTECTKKTDKPEEPMCXHNQDRFBYIYKIRSPYTODKROGYHERYIODSWEMELGESFYPTBWMSGEJERSEYHNWZKGISSTLNURDIFSVSDMJPKOMTLABYBGTBTEFNBBYTJPKOCTPYIORDURLRASSKFMTTMKCNFLLNVWWPTSBAGWTTPYMUOELP",
			},
			"crypto-output",
			"d90191d90197a201020283d9012fa602f403582103a9394a2f1a4f99613a716956c8540f6dba6f18931c2639107221b267d740af23045820dbe80cbb4e0e418b06f470d2afe7a8c17be701ab206c59a65e65a824016a6c7005d90131a20100020006d90130a301881830f500f500f502f5021a5a0804e30304081ac7bce7a8d9012fa602f4035821022196adc25fde169fe92e70769059102275d2b40cc98776eaab92b82a86135e92045820438eff7b3b36b6d11a60a22ccb9306eea305b0439f1ea09d5928015de373811605d90131a20100020006d90130a301881830f500f500f502f5021add4fadee0304081a22969377d9012fa602f403582102fb72507fc20ddba92991b17c4bb466130ad93a886e73175033bb43e3bc785a6d04582095b34913937fa5f1c6205b525bb57de1517625e04586b595be68e71362d3edc505d90131a20100020006d90130a301881830f500f500f502f5021a9bacd5c00304081a97ec38f9",
			2, []int{1347, 1355},
			false,
		},
		{
			[]string{
				"ur:bytes/1-9/lpadascfadaxcywenbpljkhdcahkadaemejtswhhylkepmykhhtsytsnoyoyaxaedsuttydmmhhpktpmsrjtdkgslpgh",
				"ur:bytes/2-9/lpaoascfadaxcywenbpljkhdcagwdpfnsboxgwlbaawzuefywkdplrsrjynbvygabwjldapfcsgmghhkhstlrdcxaefz",
				"ur:bytes/3-9/lpaxascfadaxcywenbpljkhdcahelbknlkuejnbadmssfhfrdpsbiegecpasvssovlgeykssjykklronvsjksopdzmol",
				"ur:bytes/4-9/lpaaascfadaxcywenbpljkhdcasotkhemthydawydtaxneurlkosgwcekonertkbrlwmplssjtammdplolsbrdzcrtas",
				"ur:bytes/5-9/lpahascfadaxcywenbpljkhdcatbbdfmssrkzmcwnezelennjpfzbgmuktrhtejscktelgfpdlrkfyfwdajldejokbwf",
				"ur:bytes/6-9/lpamascfadaxcywenbpljkhdcackjlhkhybssklbwefectpfnbbectrljectpavyrolkzczcpkmwidmwoxkilghdsowp",
				"ur:bytes/7-9/lpatascfadaxcywenbpljkhdcavszmwnjkwtclrtvaynhpahrtoxmwvwatmedibkaegdosftvandiodagdhthtrlnnhy",
				"ur:bytes/8-9/lpayascfadaxcywenbpljkhdcadmsponkkbbhgsoltjntegepmttmoonftnbuoiyrehfrtsabzsttorodklubbuyaetk",
				"ur:bytes/9-9/lpasascfadaxcywenbpljkhdcajskecpmdckihdyhphfotjojtfmlnwmadspaxrkytbztpbauotbgtgtaeaevtgavtny",
				"ur:bytes/10-9/lpbkascfadaxcywenbpljkhdcahkadaemejtswhhylkepmykhhtsytsnoyoyaxaedsuttydmmhhpktpmsrjtwdkiplzs",
				"ur:bytes/11-9/lpbdascfadaxcywenbpljkhdcahelbknlkuejnbadmssfhfrdpsbiegecpasvssovlgeykssjykklronvsjkvetiiapk",
				"ur:bytes/12-9/lpbnascfadaxcywenbpljkhdcarllaluzmdmgstospeyiefmwejlwtpedamktksrvlcygmzemovovllarodtmtbnptrs",
				"ur:bytes/13-9/lpbtascfadaxcywenbpljkhdcamtkgtpknghchchyketwsvwgwfdhpgmgtylctotzopdrpayoschcmhplffziachrfgd",
				"ur:bytes/14-9/lpbaascfadaxcywenbpljkhdcapazewnvonnvdnsbyleynwtnsjkjndeoldydkbkdslgjkbbkortbelomueekgvstegt",
				"ur:bytes/15-9/lpbsascfadaxcywenbpljkhdcaynmhpddpzmversbdqdfyrehnqzlugmjzmnmtwmrouohtstgsbsahpawkditkckynwt",
				"ur:bytes/16-9/lpbeascfadaxcywenbpljkhdcawygekobamwtlihsnpalnsghenskkiynthdzotsimtojetprsttmukirlrsbtamjtpd",
				"ur:bytes/17-9/lpbyascfadaxcywenbpljkhdcamklgftaxykpewyrtqzhydntpnytyisincxmhtbceaykolduortotiaiaiafhiaoyce",
				"ur:bytes/18-9/lpbgascfadaxcywenbpljkhdcahkadaemejtswhhylkepmykhhtsytsnoyoyaxaedsuttydmmhhpktpmsrjtntwkbkwy",
				"ur:bytes/19-9/lpbwascfadaxcywenbpljkhdcadekicpaajootjzpsdrbalpeywllbdsnbinaerkurspbncxgslgftvtsrjtksplcpeo",
				"ur:bytes/20-9/lpbbascfadaxcywenbpljkhdcayapmrleeleaxpasfrtrdkncffwjyjzgyetdmlewtkpktgllepfrltataztksmhkbot",
			},
			"bytes", "590100916ec65cf77cadf55cd7f9cda1a1030026ddd42e905b77adc36e4f2d3ccba44f7f04f2de44f42d84c374a0e149136f25b01852545961d55f7f7a8cde6d0e2ec43f3b2dcb644a2209e8c9e34af5c4747984a5e873c9cf5f965e25ee29039fdf8ca74f1c769fc07eb7ebaec46e0695aea6cbd60b3ec4bbff1b9ffe8a9e7240129377b9d3711ed38d412fbb4442256f1e6f595e0fc57fed451fb0a0101fb76b1fb1e1b88cfdfdaa946294a47de8fff173f021c0e6f65b05c0a494e50791270a0050a73ae69b6725505a2ec8a5791457c9876dd34aadd192a53aa0dc66b556c0c215c7ceb8248b717c22951e65305b56a3706e3e86eb01c803bbf915d80edcd64d4d",
			9, []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20},
			false,
		},
	}
	for _, test := range tests {
		var d Decoder
		for _, ur := range test.urs {
			if err := d.Add(strings.ToLower(ur)); err != nil {
				if !test.error {
					t.Error(err)
				}
			} else {
				if test.error {
					t.Errorf("%q unexpectedly decoded successfully", ur)
				}
			}
		}
		if test.error {
			continue
		}
		typ, got, err := d.Result()
		if err != nil {
			t.Fatal(err)
		}
		if typ != test.wantType {
			t.Errorf("%q: decoded type %q, wanted %q", test.urs[0], typ, test.wantType)
		}
		want, err := hex.DecodeString(test.want)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(got, want) {
			t.Errorf("%q: decoded to %x; wanted %x", test.urs[0], got, want)
		}
		for i, seqNum := range test.seqNums {
			got := Encode(test.wantType, want, seqNum, test.seqLen)
			want := strings.ToLower(test.urs[i])
			if want != got {
				t.Errorf("seqNum %d of %s is %s expected %s", seqNum, test.want, got, want)
			}
		}
	}
}

func TestSplit(t *testing.T) {
	t.Parallel()

	maxShares := 15
	if testing.Short() {
		maxShares = 10
	}
	data := make([]byte, 10e3)
	for i := range data {
		data[i] = byte(i)
	}
	for n := 1; n <= maxShares; n++ {
		name := fmt.Sprintf("%d-shares", n)
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			for m := 1; m <= n; m++ {
				data := Data{
					Data:      data,
					Threshold: m,
					Shards:    n,
				}
				if !recoverable(data) {
					t.Errorf("%d-of-%d: failed to recover", m, n)
				}
			}
		})
	}
}

func recoverable(data Data) bool {
	var shares [][]string
	for k := range data.Shards {
		shares = append(shares, Split(data, k))
	}
	// Count to all bit patterns of n length, choose the ones with
	// m bits.
	allPerm := uint64(1)<<data.Shards - 1
	for c := uint64(1); c <= allPerm; c++ {
		if bits.OnesCount64(c) != data.Threshold {
			continue
		}
		c := c
		d := new(Decoder)
		for c != 0 {
			share := bits.TrailingZeros64(c)
			c &^= 1 << share
			for _, ur := range shares[share] {
				d.Add(ur)
			}
		}
		_, enc, err := d.Result()
		if err != nil {
			return false
		}
		if enc == nil {
			return false
		}
		return bytes.Equal(enc, data.Data)
	}
	return true
}
