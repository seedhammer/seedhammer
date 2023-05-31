package address

import (
	"testing"

	"github.com/btcsuite/btcd/chaincfg"
	"seedhammer.com/nonstandard"
)

func TestAddresses(t *testing.T) {
	xpubs := []string{
		"xpub6DiYrfRwNnjeX4vHsWMajJVFKrbEEnu8gAW9vDuQzgTWEsEHE16sGWeXXUV1LBWQE1yCTmeprSNcqZ3W74hqVdgDbtYHUv3eM4W2TEUhpan",
		"xpub6DjrnfAyuonMaboEb3ZQZzhQ2ZEgaKV2r64BFmqymZqJqviLTe1JzMr2X2RfQF892RH7MyYUbcy77R7pPu1P71xoj8cDUMNhAMGYzKR4noZ",
		"xpub6DnT4E1fT8VxuAZW29avMjr5i99aYTHBp9d7fiLnpL5t4JEprQqPMbTw7k7rh5tZZ2F5g8PJpssqrZoebzBChaiJrmEvWwUTEMAbHsY39Ge",
	}
	tests := []struct {
		desc     string
		receives []string
		changes  []string
	}{
		{
			"pkh(" + xpubs[0] + ")",
			[]string{"1M88vKcJFc4KPAe5RHXsuJqWcg3muStyK4", "1DyJom6LUg98zbcff7Y3vnh6kYpERcMys3", "1HPR4dJ2W4i9Q4FnkyYGs41d1CczxQuwiA"},
			[]string{"12fk5WJ9AtzQzRWFtCabn8Wh45zmjmcpFR", "1A6QmCc5cqhtyzmgMmEKFWc7eP8mvyUcFJ", "18vo9Lf4vaGUzgji8bGQ1LQ5zxU5yh2DDB"},
		},
		{
			"wpkh(" + xpubs[0] + ")",
			[]string{"bc1qmj7qns4exnh8p6a9xndvz34msj72arnxl3sapx", "bc1q3er64jwge5sfezr6ymkt6d9l79zcvs8z20n5xz", "bc1qkwl5qpx6k93cqmnygn6kgucgka8q3z4kur2nm8"},
			[]string{"bc1qzf97gj5h2ryu2f8lpx8940dkn4vk8g6xx3gwlg", "bc1qvwlscfgdmtkna074wylrvqly4w6nlpklsmyx7x", "bc1q2m6hyqsnxwqp6f0mlcp6yh896rsmqw3ugj26hr"},
		},
		{
			"sh(wpkh(" + xpubs[0] + "))",
			[]string{"354hXbgwGRqHXywh9ZESRXWW4zxrpeScXQ", "37cG1ZYNKcQYikRkdmJKKKfXxiVbk6ywiJ", "3KwWvmB9DsRJLGt11ozWLPsdbw5GfbAqjb"},
			[]string{"35c95EWSNQJCyh7uNVZ4rp2hf41GUsgdLn", "3GWo6g2n5iBwtadHgJqYyL1UMEvAwSTUg7", "3Ho1jfnTtDaW5isJfgjMY3v3rQMwDDyVQt"},
		},
		{
			"tr(" + xpubs[0] + ")",
			[]string{"bc1ppeya86zv0hnpzrvh7czgqxkn5zjxxymxd6nqplhhx7fejxvhk0ysp7zekg", "bc1pqhh2d3sdktkfvneee95mlv99t0cddcy3vpk5fglz78jm3e55zydqj5wycf", "bc1px4k4y20vusff4v0xvpgwslda2s2fuajmn8eypt28ae4r73jlut7s8y5tq6"},
			[]string{"bc1px5xqncrjm3823nervn3epj2al0adt79aaa56jvxpvzy29stvjn2q2jruge", "bc1p5u5rrr4lczraxkq3xwdjxh98fkl4sjuswwxgwj2uw3rdfwjp8uusp2ymfr", "bc1pvhsgwwmthv864r4kt2g65323jau4ge0y4k4qufqjzvfzsk4d60fq7pe6xx"},
		},
		{
			"wsh(sortedmulti(1," + xpubs[0] + "))",
			[]string{"bc1qm78sug9d6g4jwlk9qulgtcp9ghepn2xjfz8xdhpa8g3q3hzcl8nsfez8at", "bc1q6uk7f77v7lspm803kjgvfpmreumdnjgaksfq3mvuhzc0zwvcy83qedrjvj", "bc1qntv6z9lyzxedfp63qgr7pm2gk9uzfjjzhhzm5j8599u6m89h2q6q3fzhu6"},
			[]string{"bc1qe3x073dtr0vy8xd342ctnsdzfz5ule53ul933jutx5yesxj3032qzmp8pj", "bc1q4yx84f5t2zgk24dcn87azhhvuxwr2psduhy4pl8vzrjv28zvazfs82u368", "bc1qxx0tjkg3qce48nvjyrnqssc9evqh25guursx7uk7uvkx6njj92vs40pp2u"},
		},
		{
			"wsh(sortedmulti(1," + xpubs[0] + "/1234/<5;6>/*))",
			[]string{"bc1qt77623mmw4lnsewlmt9cs60yvxpwks540ygtzkakdf8xaa4ahsvqcma0k0", "bc1qz6qz4m3uj40cpqt6s4nmg9jew66qzmthrun95mxy5662u3ldwdaqj8edge"},
			[]string{"bc1qc8gz4sw524pje9lwz5ujjrxvha774e8w6a2xul4jt8eed7h7hcvsc6cm4y", "bc1qwh9lhlgx9an4kz3s9qtrfm3xyvms84lkjy4paflg408vswjq4zcqx2xzlp"},
		},
		{
			"sh(wsh(sortedmulti(2," + xpubs[0] + "," + xpubs[1] + "," + xpubs[2] + ")))",
			[]string{"3EECinK7zYPwa4bR53mbGiuLrbU2V9waHg", "34EqecNrmzM2v2Qx7MvaU49FEsdpxjsRw3"},
			[]string{"3L7AnrmQiSuAPNTX73d8zdfu5o2hUe3V6C", "3Hp5QsDqFGpDoYfiBV1uPctKE5MYaxfqNK"},
		},
		{
			"sh(sortedmulti(2," + xpubs[0] + "," + xpubs[1] + "," + xpubs[2] + "))",
			[]string{"3DwWNBMDdsP5Tf9wYyGT7qMkCEe5mTC3U3", "334QzbkBDRWfBWuE8Qhj5dXigYZpt7tpcT"},
			[]string{"39DByP7DcYyQHLhwYewbnN92e2T9Nz4n81", "3DwUtJerhAjkm2UALCkQkNFnrPgFmMZ9hT"},
		},
		{
			// Non-sorted variant.
			"wsh(multi(2," + xpubs[0] + "," + xpubs[1] + "," + xpubs[2] + "))",
			[]string{"bc1q4taqq6q6l8fvguva6ftvrz3qgdjy6p3w2s0ds0nl6qrjw7t0hfhqgrqcwd", "bc1qw3nhtat85lz6g3f8dh42067gf25hzquzn0tx9nk9nv2t6wtlx9lsfz7z0n"},
			nil,
		},
	}
	for _, test := range tests {
		desc, err := nonstandard.OutputDescriptor([]byte(test.desc))
		if err != nil {
			t.Fatalf("%s: %v", test.desc, err)
		}
		for i, want := range test.receives {
			got, err := Receive(&chaincfg.MainNetParams, desc, uint32(i))
			if err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Errorf("descriptor %s: got address %d:%s, want %s", test.desc, i, got, want)
			}
		}
		for i, want := range test.changes {
			got, err := Change(&chaincfg.MainNetParams, desc, uint32(i))
			if err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Errorf("descriptor %s: got change address %d:%s, want %s", test.desc, i, got, want)
			}
		}
	}
}
