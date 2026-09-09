# SubGlance — Designsysteem

> Interne bron van waarheid voor visuele en interactiekeuzes. Zie
> `ARCHITECTURE.md` voor de technische keuzes.
>
> **Levende voorbeelden:** open `docs/mockups/index.html` in een browser. Elke
> regel hieronder is daar te zien en aan te klikken. Wijkt de code af van dit
> document, dan is dit document leidend — of het is verouderd en moet het
> bijgewerkt worden in dezelfde PR.

---

## 1. Uitgangspunt

SubGlance beantwoordt één vraag: **werkt het nog?** Alles in de interface
verdient zijn plek door die vraag sneller te beantwoorden, of het hoort er niet.

Het product concurreert met Uptime Kuma en Better Stack. Onze inzet is niet meer
functies, maar een dashboard dat je een halve seconde hoeft aan te kijken. Dat is
een designclaim, en die moet het ontwerp dus waarmaken.

### De vijf regels

1. **Status is kleur, activiteit is beweging.** De led vertelt wát er aan de hand
   is. Beweging vertelt dát er iets gebeurde. Zodra die twee door elkaar lopen,
   betekent geen van beide nog iets.
2. **Stilte is de standaard.** Een gezond dashboard hoort saai te zijn. Alles dat
   permanent pulseert, knippert of kleurt, leert de kijker om weg te kijken —
   precies op het moment dat je zijn aandacht nodig hebt.
3. **Afwezigheid is luid.** Een mislukte check tekent op volle hoogte. Wat er
   níét is, mag er niet uitzien als een snelle response.
4. **Eén signaal per betekenis.** Zodra twee dingen hetzelfde zeggen, verwatert
   het signaal dat ertoe doet.
5. **Grootte is voor structuur, niet voor nadruk.** Nadruk komt van kleur en
   contrast. Componenten die per weergave van maat veranderen, houden op
   referentiepunten te zijn.

---

## 2. Tokens

Alle waarden zijn CSS custom properties. Bij de overstap naar Tailwind v4 worden
dit `@theme`-tokens; de namen blijven gelijk. **Hardcode nooit een kleur of
radius in een component.**

### 2.1 Kleur — donker (standaard)

```css
--canvas:     #08090a;   /* paginaachtergrond */
--surface:    #0e1011;   /* kaart, sidebar, drawer */
--surface-2:  #141719;   /* invoervelden, hover */
--surface-hi: #1a1e20;   /* actieve segmenten, tracks */
--border:     #1e2224;   /* standaard rand, scheidingslijn */
--border-hi:  #2a2f32;   /* rand van interactieve elementen */

--ink:        #e8eaec;   /* primaire tekst */
--ink-2:      #9ba1a6;   /* secundaire tekst */
--ink-3:      #61686d;   /* labels, hulptekst */
--ink-4:      #3d4347;   /* placeholders, uitgeschakeld */
```

### 2.2 Kleur — licht

```css
--canvas:     #fbfbfa;   --surface:    #ffffff;
--surface-2:  #f6f6f5;   --surface-hi: #f0f0ef;
--border:     #e6e6e4;   --border-hi:  #d6d6d3;
--ink:        #16181a;   --ink-2:      #5c6165;
--ink-3:      #8b9196;   --ink-4:      #b6bbbf;
```

Licht is geen omgekeerd donker. `--canvas` is warmgrijs (`#fbfbfa`), niet wit;
kaarten zijn wél wit. Zo staan kaarten vóór de pagina in plaats van erin te
verdwijnen. De statuskleuren zijn in licht donkerder en verzadigder, omdat het
donkere origineel op wit onleesbaar wordt.

### 2.3 Status

| Status | Donker | Licht | Betekenis |
|---|---|---|---|
| `--up` | `#34d399` | `#059669` | Laatste check geslaagd |
| `--warn` | `#fbbf24` | `#b45309` | Traag, of certificaat verloopt |
| `--down` | `#f43f5e` | `#e11d48` | Laatste check mislukt |
| `--idle` | `#4b5257` | `#c2c7cb` | Gepauzeerd, of nog geen data |

Elke statuskleur heeft een `-dim`-variant voor achtergronden van badges en
rijen (`--up-dim`, `--warn-dim`, `--down-dim`).

**Kleur staat nooit alleen.** Ongeveer 8% van de mannen ziet rood en groen niet
betrouwbaar uit elkaar — voor een product dat draait om rood-versus-groen is dat
geen randgeval. Status wordt daarom altijd dubbel gedragen: kleur plus positie
(kapot sorteert naar boven), kleur plus vorm (de hoogte van de heartbeat-balk),
of kleur plus tekst (`502 Bad Gateway` in plaats van alleen rood).

### 2.4 Gloed

```css
--glow-up:   0 0 0 1px rgba(52,211,153,.35), 0 0 7px -1px rgba(52,211,153,.6);
--glow-warn: 0 0 0 1px rgba(251,191,36,.38), 0 0 7px -1px rgba(251,191,36,.62);
--glow-down: 0 0 0 1px rgba(244,63,94,.4),   0 0 8px -1px rgba(244,63,94,.72);
```

Een strakke ring van 1px plus een korte bloom. Een brede zachte gloed leest op
dit formaat als een onscherpe vlek; de ring houdt de rand van de lamp leesbaar
terwijl hij toch verlicht oogt.

### 2.5 Typografie

```css
--font-sans: "Geist", ui-sans-serif, system-ui, -apple-system, sans-serif;
--font-mono: "Geist Mono", ui-monospace, "SF Mono", Menlo, monospace;
```

| Rol | Grootte | Gewicht |
|---|---|---|
| Paginatitel | 20–22px | 500, `letter-spacing: -.02em` |
| Kaarttitel | 15px | 500 |
| Rijtitel | 13.5px | 450 |
| Body / label | 12.5–13px | 400–450 |
| Hulptekst | 11.5px | 400 |
| Sectiekop | 12px | 500, uppercase, `letter-spacing: .09em` |

**Mono is verplicht voor alles wat meetbaar is:** URL's, latency, uptime,
intervallen, statuscodes. Cijfers die naast elkaar in een kolom staan moeten
uitlijnen — anders vergelijk je ze niet, je leest ze.

### 2.6 Vorm, ruimte, beweging

```css
--r-sm: 6px;    /* knoppen, invoer, kleine controls */
--r-md: 10px;   /* rijen, lijstitems */
--r-lg: 14px;   /* kaarten, dialogen, drawer */

--ease: cubic-bezier(.32, .72, 0, 1);
--dur:  420ms;  /* thema-overgang */
```

Ruimte gaat in stappen van 4px. Duur naar rol: 140ms voor hover, 200–280ms voor
panelen, 320–520ms voor iets dat je aandacht vraagt. `--ease` start snel en komt
zacht tot stilstand — beweging die uitdempt voelt mechanisch, niet zwevend.

---

## 3. De led

Het merkicoon van het product. Als er één component goed moet zijn, is dit het.

```
20 × 7 px   ·   border-radius: 2.5px   ·   één maat, overal
```

**Waarom een pill en geen stip.** Een horizontale vorm leest als een lampje in
apparatuur; een cirkel leest als een bolletje in een lijst. Het verschil is klein
en het bepaalt of het dashboard aanvoelt als instrumentarium of als een webpagina.

**Waarom 2.5px en niet volledig rond.** Bij `999px` heeft het oog geen rechte
lijn om op scherp te stellen en oogt de vorm vaag. 2.5px houdt de pill-vorm maar
geeft de zijkanten een leesbare rand.

**De highlight.** Een verloop van boven, `inset: 1px 1px 3px`, opacity `.5`. Het
suggereert een gebogen lens die licht vangt. Zonder highlight is het een gekleurd
rechthoekje; met een te sterke highlight is het een knopje.

**Regels.**

- Eén maat in de hele applicatie — sidebar, rijen, kaarten, groepskoppen, toasts.
- De led knippert **nooit** bij een routinecheck. Alleen bij een echte
  statuswijziging speelt hij één korte overgang (`.changed`), en dan is het klaar.
- Geen pulserende animatie in rust. Veertien pulserende lampjes zijn geen
  dashboard maar een screensaver.

---

## 4. De heartbeat-balk

Draagt twee dingen tegelijk, en dat is bewust:

- **Kleur = status** van die check.
- **Hoogte = latency**, relatief aan het bereik van die monitor.

Eén blik beantwoordt dus zowel "draait het" als "wordt het trager". Een
mislukte check tekent **op volle hoogte** — afwezigheid moet luid zijn, niet
lijken op een snelle response.

Nieuwste check staat rechts. De laatste 28 checks zijn zichtbaar; dat is genoeg
om een patroon te zien en weinig genoeg om per balk nog iets te onderscheiden.

---

## 5. Live checks

De scheduler draait continu; de SSE-stream levert elk resultaat binnen. Dat
tonen is het verschil tussen "deze pagina leeft" en "deze pagina is een uur
geleden stilletjes doodgegaan".

**Wat er gebeurt als een check binnenkomt:**

1. Het nieuwste heartbeat-balkje schuift in — komt binnen op 35% hoogte, schiet
   licht door, zakt terug, met een korte oplichting (520ms). De oudste balk valt weg.
2. Het latencygetal tickt mee (320ms), want dat is het getal dat de check opleverde.

**Wat er nadrukkelijk niet gebeurt:**

- De led knippert niet mee. Zie regel 1 en 4: als de led ook op elke check
  reageert, betekent knipperen niets meer.
- Er is **geen rij-brede flits of veeg**. Een tint over de hele rij is een grote,
  laagfrequente verandering die leest als "deze rij doet iets" — dat is de taal
  die gereserveerd is voor een statuswijziging. Dit is geprobeerd en bewust
  teruggedraaid.

Het effect is uitschakelbaar via de layoutinstellingen ("Show live checks",
onthouden per gebruiker) en gaat automatisch uit bij `prefers-reduced-motion`.

---

## 6. Layouts

De layout is een **gebruikersinstelling**, geen ontwerpbesluit dat wij voor
iedereen nemen. Vier weergaven van dezelfde data, te kiezen via het grid-icoon
in de toolbar, onthouden per gebruiker.

| Layout | Voor | Kenmerk |
|---|---|---|
| **Rows** | Laptop, dagelijks scannen | Eén rij per monitor, kapot sorteert naar boven |
| **Cards** | Tweede scherm op het bureau | Tegels, grotere leds, minder woorden |
| **Compact** | 100+ monitors | Gegroepeerd per klant/omgeving, één regel per monitor |
| **Status wall** | Scherm aan de muur | Alleen led en naam, verder niets |

**Status wall** verbergt sidebar én topbar volledig. Dat is de hele reden dat
hij bestaat. Twee dingen blijven: een fluisterregel met naam en telling, en een
klok. De klok is geen versiering — op een scherm dat bijna nooit verandert is een
tikkende seconde het enige bewijs dat je naar iets levends kijkt.

In Status wall krijgen kapotte kaarten een **warme rand**, geen gekleurde
vulling. Een muur vol gekleurde kaarten is ruis; een muur stille kaarten met twee
warme randen is informatie.

**Sidebar.** Inklapbaar in alle layouts, via de knop links in de topbar of
`Cmd/Ctrl + B`. Ingeklapt wordt het een rail van 56px met alleen iconen — niet
weg, want dan is je navigatie onbereikbaar. Status wall verbergt hem volledig en
onthoudt jouw voorkeur apart, zodat je bij terugkeer de rail terugkrijgt zoals
je hem had.

---

## 7. Componenten

Zie `docs/mockups/components.html` voor de werkende versie van alles hieronder.

### 7.1 Knoppen

| Variant | Gebruik |
|---|---|
| `primary` | De ene actie die het scherm bedoelt. Eén per scherm. |
| `secondary` | Nevenacties met gelijk gewicht |
| `ghost` | Annuleren, sluiten, alles wat de gebruiker terugbrengt |
| `danger` | Verwijderen en onomkeerbare acties |

**Destructief is een rode omlijning, geen rood vlak.** Een fel rode knop die
permanent in beeld staat wordt behang — en dan klikt iemand hem per ongeluk. Het
vlak vult pas bij hover, wanneer de intentie er al is.

Laadstaat: de knop houdt zijn breedte en toont een spinner in plaats van tekst.
Zo springt de layout niet op het moment dat je klikt.

### 7.2 Formulieren

De **60-secondentest** is de norm: URL plakken, al het andere heeft een
verstandige default. Elk verplicht veld dat we toevoegen kost installaties.

- Focus is een groene rand plus een ring van 3px op 14% — zichtbaar zonder de
  blauwe browserglow.
- Fouten staan onder het veld, met icoon, en noemen het juiste formaat in plaats
  van alleen te melden dat het fout is.
- Eenheden (`sec`, `ms`) horen in een addon aan het veld, niet in het label.
- Drie tot vier elkaar uitsluitende opties: segmented control, geen dropdown.
  Zichtbare opties zijn goedkoper te lezen dan verborgen opties.

### 7.3 Schakelaars

Een toggle is voor iets dat direct effect heeft (monitor aan/uit). Een checkbox
is voor een keuze die pas telt bij opslaan. De thumb reist, de track vult —
anders klikt iemand twee keer omdat hij niet ziet of het aankwam.

### 7.4 Inline bewerken

Een monitor hernoemen hoort geen modal te zijn. Klik de naam, typ, `Enter`
bevestigt, `Esc` zet terug. Bij hover verschijnt een subtiel potlood; zonder dat
weet niemand dat het kan.

### 7.5 Destructieve bevestiging

Verwijderen vraagt om de naam **overgetypt**, niet om een "weet je het zeker?".
Het tweede is een reflex, het eerste een beslissing. De dialoog benoemt precies
wat er verdwijnt, inclusief openstaande incidenten.

### 7.6 Lege, ladende en foutstaten

Dit is het verschil tussen af en bijna af.

- **Leeg dashboard is de onboarding.** Het is het eerste wat een nieuwe
  self-hoster ziet. Het verdient een echt ontwerp, geen schouderophalen.
- **Skeletons, geen spinners.** Een spinner kondigt een wachttijd aan die er niet
  is. Skeletons houden de pagina stil.
- **Toasts bevestigen, ze informeren niet.** Verdwijnen na 5 seconden en dragen
  nooit informatie die je nog nodig hebt.

### 7.7 Commandopalet

`Cmd/Ctrl + K`. Bij 100+ monitors is zoeken sneller dan scrollen. Bevat zowel
monitors als acties, want de gebruiker weet niet welke van de twee hij zoekt.

---

## 8. Toegankelijkheid

Geen sluitpost — een aantal keuzes hierboven bestaat er juist om.

- **Nooit alleen kleur.** Zie §2.3.
- **Contrast:** `--ink` en `--ink-2` halen AA op hun achtergrond. `--ink-3` is
  voor labels en hulptekst, `--ink-4` uitsluitend voor placeholders en
  uitgeschakelde staat — nooit voor tekst die gelezen moet worden.
- **Focus is altijd zichtbaar.** `:focus-visible` krijgt een ring, ook op
  toggles en aangepaste controls. Nooit `outline: none` zonder vervanging.
- **`prefers-reduced-motion`** schakelt live checks, statusovergangen en alle
  transitions uit. De informatie blijft, alleen de beweging verdwijnt.
- Toetsenbord: `Esc` sluit drawer, dialoog en palet, en verlaat Status wall.
  Alles wat klikbaar is moet bereikbaar zijn met Tab.

---

## 9. Wat we niet doen

Even belangrijk als de rest, want dit zijn de dingen die terugkomen:

- **Geen pulserende animaties in rust.** Zie regel 2.
- **Geen kant-en-klare chartlibrary.** De heartbeat-balk is ons merkicoon;
  generieke charts zien er generiek uit.
- **Geen standaard shadcn-uiterlijk.** shadcn/ui is het startpunt, niet het
  eindpunt. Als het eruitziet als elk ander shadcn-dashboard, is het mislukt.
- **Geen kleurvlakken voor status waar een rand voldoet.** Zie §6.
- **Geen instelling die maar in één weergave werkt.** Een S/M/L-dichtheidsregelaar
  is geprobeerd en verwijderd: hij deed alleen iets in Status wall, en een knop
  die meestal niets doet leert mensen dat knoppen niets doen.

---

## 10. Van mockup naar code

De mockups zijn losse HTML-bestanden zonder build. Bij de implementatie in
React + Tailwind v4:

1. **Tokens eerst.** §2 wordt één `@theme`-blok. Geen enkele component bevat een
   letterlijke hexwaarde.
2. **De led wordt een component**, geen utility-klassen. Eén plek waar formaat,
   radius, highlight en gloed vastliggen.
3. **Layouts zijn een gebruikersinstelling** in de database, geen route.
4. **Live checks komen uit de bestaande SSE-stream.** De simulatie in de mockup
   (`landCheck()`) laat zien wat er per binnengekomen resultaat moet gebeuren.
5. Dit document wordt bijgewerkt in dezelfde PR als de afwijking. Een styleguide
   die achterloopt is erger dan geen styleguide.
