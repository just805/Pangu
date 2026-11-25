1:"$Sreact.fragment"
b:I[563491,["/_next/static/chunks/3mi1ad2dacb47.js","/_next/static/chunks/0wd_oeyklqnyx.js","/_next/static/chunks/1ntn7efqc-iiw.js","/_next/static/chunks/04u9x8_fzw9b5.js","/_next/static/chunks/35ifdzvyefy82.js","/_next/static/chunks/09z1tte0k-n3_.js"],"default"]
:HL["/_next/static/chunks/4384baud_6ozc.css","style"]
:HL["/_next/static/chunks/2q72uqdvdxfu5.css","style"]
:HL["/_next/static/media/GeistMono_Variable-s.p.3ms9vq719j3f8.woff2","font",{"crossOrigin":"","type":"font/woff2"}]
:HL["/_next/static/media/GeistPixel_Circle.p.0rqe0db4f0cia.woff2","font",{"crossOrigin":"","type":"font/woff2"}]
:HL["/_next/static/media/GeistPixel_Grid.p.2ss63b6eqyzy4.woff2","font",{"crossOrigin":"","type":"font/woff2"}]
:HL["/_next/static/media/GeistPixel_Line.p.1gvmpev-r-ty_.woff2","font",{"crossOrigin":"","type":"font/woff2"}]
:HL["/_next/static/media/GeistPixel_Square.p.2hsizu-z_gncm.woff2","font",{"crossOrigin":"","type":"font/woff2"}]
:HL["/_next/static/media/GeistPixel_Triangle.p.04xw1z5oncddb.woff2","font",{"crossOrigin":"","type":"font/woff2"}]
:HL["/_next/static/media/Geist_Variable-s.p.0mrjj4bg00-he.woff2","font",{"crossOrigin":"","type":"font/woff2"}]
2:Td56,
    (function () {
      try {
        var root = document.documentElement;
        var PERSISTENCE = {"theme_mode":"client-cookie","theme_preset":"client-cookie","font":"client-cookie","content_layout":"client-cookie","navbar_style":"client-cookie","sidebar_variant":"client-cookie","sidebar_collapsible":"client-cookie"};
        var DEFAULTS = {"theme_mode":"light","theme_preset":"default","font":"geist","content_layout":"full-width","navbar_style":"sticky","sidebar_variant":"floating","sidebar_collapsible":"icon"};

        function readCookie(name) {
          var match = document.cookie.split("; ").find(function(c) {
            return c.startsWith(name + "=");
          });
          return match ? decodeURIComponent(match.split("=")[1]) : null;
        }

        function readLocal(name) {
          try {
            return window.localStorage.getItem(name);
          } catch (e) {
            return null;
          }
        }

        function readPreference(key, fallback) {
          var mode = PERSISTENCE[key];
          var value = null;

          if (mode === "localStorage") {
            value = readLocal(key);
          }

          if (!value && (mode === "client-cookie" || mode === "server-cookie")) {
            value = readCookie(key);
          }

          if (!value || typeof value !== "string") {
            return fallback;
          }

          return value;
        }

        var rawMode = readPreference("theme_mode", DEFAULTS.theme_mode);
        var rawPreset = readPreference("theme_preset", DEFAULTS.theme_preset);
        var rawFont = readPreference("font", DEFAULTS.font);
        var rawContentLayout = readPreference("content_layout", DEFAULTS.content_layout);
        var rawNavbarStyle = readPreference("navbar_style", DEFAULTS.navbar_style);
        var rawSidebarVariant = readPreference("sidebar_variant", DEFAULTS.sidebar_variant);
        var rawSidebarCollapsible = readPreference("sidebar_collapsible", DEFAULTS.sidebar_collapsible);

        var isValidMode = rawMode === "dark" || rawMode === "light" || rawMode === "system";
        var mode = isValidMode ? rawMode : DEFAULTS.theme_mode;
        var resolvedMode =
          mode === "system" && window.matchMedia
            ? (window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light")
            : mode;
        var preset = rawPreset || DEFAULTS.theme_preset;
        var font = rawFont || DEFAULTS.font;
        var contentLayout = rawContentLayout || DEFAULTS.content_layout;
        var navbarStyle = rawNavbarStyle || DEFAULTS.navbar_style;
        var sidebarVariant = rawSidebarVariant || DEFAULTS.sidebar_variant;
        var sidebarCollapsible = rawSidebarCollapsible || DEFAULTS.sidebar_collapsible;

        root.classList.toggle("dark", resolvedMode === "dark");
        root.setAttribute("data-theme-mode", mode);
        root.setAttribute("data-theme-preset", preset);
        root.setAttribute("data-font", font);
        root.setAttribute("data-content-layout", contentLayout);
        root.setAttribute("data-navbar-style", navbarStyle);
        root.setAttribute("data-sidebar-variant", sidebarVariant);
        root.setAttribute("data-sidebar-collapsible", sidebarCollapsible);

        root.style.colorScheme = resolvedMode === "dark" ? "dark" : "light";

      } catch (e) {
        console.warn("ThemeBootScript error:", e);
      }
    })();
  0:{"P":null,"c":["","system","agents","detail",""],"q":"","i":false,"f":[[["",{"children":["(main)",{"children":["system",{"children":["agents",{"children":["detail",{"children":["__PAGE__",{}]}]}]}]}]},"$undefined","$undefined",16],[["$","$1","c",{"children":[[["$","link","0",{"rel":"stylesheet","href":"/_next/static/chunks/4384baud_6ozc.css","precedence":"next","crossOrigin":"$undefined","nonce":"$undefined"}],["$","link","1",{"rel":"stylesheet","href":"/_next/static/chunks/2q72uqdvdxfu5.css","precedence":"next","crossOrigin":"$undefined","nonce":"$undefined"}],["$","script","script-0",{"src":"/_next/static/chunks/3mi1ad2dacb47.js","async":true,"nonce":"$undefined"}],["$","script","script-1",{"src":"/_next/static/chunks/0wd_oeyklqnyx.js","async":true,"nonce":"$undefined"}],["$","script","script-2",{"src":"/_next/static/chunks/1ntn7efqc-iiw.js","async":true,"nonce":"$undefined"}],["$","script","script-3",{"src":"/_next/static/chunks/04u9x8_fzw9b5.js","async":true,"nonce":"$undefined"}],["$","script","script-4",{"src":"/_next/static/chunks/35ifdzvyefy82.js","async":true,"nonce":"$undefined"}]],["$","html",null,{"lang":"en","data-theme-mode":"light","data-theme-preset":"default","data-content-layout":"full-width","data-navbar-style":"sticky","data-sidebar-variant":"floating","data-sidebar-collapsible":"icon","data-font":"geist","suppressHydrationWarning":true,"children":[["$","head",null,{"children":["$","script",null,{"dangerouslySetInnerHTML":{"__html":"$2"}}]}],"$L3"]}]]}],{"children":["$L4",{"children":["$L5",{"children":["$L6",{"children":["$L7",{"children":["$L8",{},null,false,null]},null,false,"$@9"]},null,false,"$@9"]},null,false,"$@9"]},null,false,null]},null,false,null],"$La",false]],"m":"$undefined","G":["$b",["$Lc","$Ld"]],"S":true,"h":null,"s":"$undefined","l":"$undefined","p":"$undefined","d":"$undefined","b":"DmH-asy8PtMbaPbhCBrbF"}
e:I[746798,["/_next/static/chunks/3mi1ad2dacb47.js","/_next/static/chunks/0wd_oeyklqnyx.js","/_next/static/chunks/1ntn7efqc-iiw.js","/_next/static/chunks/04u9x8_fzw9b5.js","/_next/static/chunks/35ifdzvyefy82.js"],"TooltipProvider"]
f:I[651027,["/_next/static/chunks/3mi1ad2dacb47.js","/_next/static/chunks/0wd_oeyklqnyx.js","/_next/static/chunks/1ntn7efqc-iiw.js","/_next/static/chunks/04u9x8_fzw9b5.js","/_next/static/chunks/35ifdzvyefy82.js"],"PreferencesStoreProvider"]
10:I[339756,["/_next/static/chunks/3mi1ad2dacb47.js","/_next/static/chunks/0wd_oeyklqnyx.js","/_next/static/chunks/1ntn7efqc-iiw.js","/_next/static/chunks/04u9x8_fzw9b5.js","/_next/static/chunks/35ifdzvyefy82.js"],"default"]
11:I[758298,["/_next/static/chunks/3mi1ad2dacb47.js","/_next/static/chunks/0wd_oeyklqnyx.js","/_next/static/chunks/1ntn7efqc-iiw.js","/_next/static/chunks/04u9x8_fzw9b5.js","/_next/static/chunks/35ifdzvyefy82.js","/_next/static/chunks/0vlctg1xsfazr.js"],"default"]
12:I[837457,["/_next/static/chunks/3mi1ad2dacb47.js","/_next/static/chunks/0wd_oeyklqnyx.js","/_next/static/chunks/1ntn7efqc-iiw.js","/_next/static/chunks/04u9x8_fzw9b5.js","/_next/static/chunks/35ifdzvyefy82.js"],"default"]
13:I[329306,["/_next/static/chunks/3mi1ad2dacb47.js","/_next/static/chunks/0wd_oeyklqnyx.js","/_next/static/chunks/1ntn7efqc-iiw.js","/_next/static/chunks/04u9x8_fzw9b5.js","/_next/static/chunks/35ifdzvyefy82.js","/_next/static/chunks/2l0u2prt781-1.js"],"default"]
14:I[713354,["/_next/static/chunks/3mi1ad2dacb47.js","/_next/static/chunks/0wd_oeyklqnyx.js","/_next/static/chunks/1ntn7efqc-iiw.js","/_next/static/chunks/04u9x8_fzw9b5.js","/_next/static/chunks/35ifdzvyefy82.js"],"Toaster"]
15:I[92825,["/_next/static/chunks/3mi1ad2dacb47.js","/_next/static/chunks/0wd_oeyklqnyx.js","/_next/static/chunks/1ntn7efqc-iiw.js","/_next/static/chunks/04u9x8_fzw9b5.js","/_next/static/chunks/35ifdzvyefy82.js"],"ClientSegmentRoot"]
16:I[749631,["/_next/static/chunks/3mi1ad2dacb47.js","/_next/static/chunks/0wd_oeyklqnyx.js","/_next/static/chunks/1ntn7efqc-iiw.js","/_next/static/chunks/04u9x8_fzw9b5.js","/_next/static/chunks/35ifdzvyefy82.js","/_next/static/chunks/059vboye02x2y.js","/_next/static/chunks/1hn03pi718mu_.js","/_next/static/chunks/31epa9yds3scr.js","/_next/static/chunks/3cjm3drbdz7t8.js","/_next/static/chunks/1e8mb4bbr2zo3.js","/_next/static/chunks/3riw0-7aeplln.js","/_next/static/chunks/03nnknec8q6ly.js","/_next/static/chunks/0t_m71oth46e3.js"],"default"]
18:I[347257,["/_next/static/chunks/3mi1ad2dacb47.js","/_next/static/chunks/0wd_oeyklqnyx.js","/_next/static/chunks/1ntn7efqc-iiw.js","/_next/static/chunks/04u9x8_fzw9b5.js","/_next/static/chunks/35ifdzvyefy82.js"],"ClientPageRoot"]
19:I[155606,["/_next/static/chunks/3mi1ad2dacb47.js","/_next/static/chunks/0wd_oeyklqnyx.js","/_next/static/chunks/1ntn7efqc-iiw.js","/_next/static/chunks/04u9x8_fzw9b5.js","/_next/static/chunks/35ifdzvyefy82.js","/_next/static/chunks/059vboye02x2y.js","/_next/static/chunks/1hn03pi718mu_.js","/_next/static/chunks/31epa9yds3scr.js","/_next/static/chunks/3cjm3drbdz7t8.js","/_next/static/chunks/1e8mb4bbr2zo3.js","/_next/static/chunks/3riw0-7aeplln.js","/_next/static/chunks/03nnknec8q6ly.js","/_next/static/chunks/0t_m71oth46e3.js","/_next/static/chunks/2_e3h_9j5rs50.js","/_next/static/chunks/176itjfm4mv-a.js","/_next/static/chunks/33frx3zlrv3f8.js"],"default"]
1c:I[897367,["/_next/static/chunks/3mi1ad2dacb47.js","/_next/static/chunks/0wd_oeyklqnyx.js","/_next/static/chunks/1ntn7efqc-iiw.js","/_next/static/chunks/04u9x8_fzw9b5.js","/_next/static/chunks/35ifdzvyefy82.js"],"OutletBoundary"]
1d:"$Sreact.suspense"
20:I[897367,["/_next/static/chunks/3mi1ad2dacb47.js","/_next/static/chunks/0wd_oeyklqnyx.js","/_next/static/chunks/1ntn7efqc-iiw.js","/_next/static/chunks/04u9x8_fzw9b5.js","/_next/static/chunks/35ifdzvyefy82.js"],"ViewportBoundary"]
22:I[897367,["/_next/static/chunks/3mi1ad2dacb47.js","/_next/static/chunks/0wd_oeyklqnyx.js","/_next/static/chunks/1ntn7efqc-iiw.js","/_next/static/chunks/04u9x8_fzw9b5.js","/_next/static/chunks/35ifdzvyefy82.js"],"MetadataBoundary"]
3:["$","body",null,{"className":"geist_df71a055-module___0gWAG__variable inter_3cf3d169-module__XNiORW__variable notosans_8c3f0bd1-module__8xuA9W__variable nunitosans_52c9c594-module__cbBv-G__variable figtree_bb498360-module__EHqQ3q__variable roboto_afbb0039-module__FoxUCa__variable raleway_b03287c2-module__6prvLa__variable dmsans_ee9b2178-module__wUnx9G__variable publicsans_be4b69c7-module__wuNFUq__variable outfit_6eb5f266-module__2mTo9a__variable geistmono_28b2d69f-module__jPT9qa__variable geistpixelsquare_cf225766-module__Yt-Tla__variable jetbrainsmono_a8025bdc-module__uoq-Yq__variable notoserif_3b849c43-module__5QnTWW__variable robotoslab_759bed3f-module__jQZ8DW__variable merriweather_8aaaab7e-module__IWx-KG__variable lora_6b4ffdf8-module__7oZ69W__variable playfairdisplay_84a48039-module__5ZZ6Fq__variable min-h-screen antialiased","children":["$","$Le",null,{"children":["$","$Lf",null,{"themeMode":"light","themePreset":"default","contentLayout":"full-width","navbarStyle":"sticky","font":"geist","children":[["$","$L10",null,{"parallelRouterKey":"children","error":"$11","errorStyles":[],"errorScripts":[["$","script","script-0",{"src":"/_next/static/chunks/0vlctg1xsfazr.js","async":true}]],"template":["$","$L12",null,{}],"templateStyles":"$undefined","templateScripts":"$undefined","notFound":[["$","$L13",null,{}],[]],"forbidden":"$undefined","unauthorized":"$undefined"}],["$","$L14",null,{}]]}]}]}]
4:["$","$1","c",{"children":[[["$","script","script-0",{"src":"/_next/static/chunks/059vboye02x2y.js","async":true,"nonce":"$undefined"}],["$","script","script-1",{"src":"/_next/static/chunks/1hn03pi718mu_.js","async":true,"nonce":"$undefined"}],["$","script","script-2",{"src":"/_next/static/chunks/31epa9yds3scr.js","async":true,"nonce":"$undefined"}],["$","script","script-3",{"src":"/_next/static/chunks/3cjm3drbdz7t8.js","async":true,"nonce":"$undefined"}],["$","script","script-4",{"src":"/_next/static/chunks/1e8mb4bbr2zo3.js","async":true,"nonce":"$undefined"}],["$","script","script-5",{"src":"/_next/static/chunks/3riw0-7aeplln.js","async":true,"nonce":"$undefined"}],["$","script","script-6",{"src":"/_next/static/chunks/03nnknec8q6ly.js","async":true,"nonce":"$undefined"}],["$","script","script-7",{"src":"/_next/static/chunks/0t_m71oth46e3.js","async":true,"nonce":"$undefined"}]],["$","$L15",null,{"Component":"$16","slots":{"children":["$","$L10",null,{"parallelRouterKey":"children","error":"$undefined","errorStyles":"$undefined","errorScripts":"$undefined","template":["$","$L12",null,{}],"templateStyles":"$undefined","templateScripts":"$undefined","notFound":[["$","$L13",null,{}],[]],"forbidden":"$undefined","unauthorized":"$undefined"}]},"serverProvidedParams":{"params":{},"promises":["$@17"]}}]]}]
5:["$","$1","c",{"children":[null,["$","$L10",null,{"parallelRouterKey":"children","error":"$undefined","errorStyles":"$undefined","errorScripts":"$undefined","template":["$","$L12",null,{}],"templateStyles":"$undefined","templateScripts":"$undefined","notFound":"$undefined","forbidden":"$undefined","unauthorized":"$undefined"}]]}]
6:["$","$1","c",{"children":[null,["$","$L10",null,{"parallelRouterKey":"children","error":"$undefined","errorStyles":"$undefined","errorScripts":"$undefined","template":["$","$L12",null,{}],"templateStyles":"$undefined","templateScripts":"$undefined","notFound":"$undefined","forbidden":"$undefined","unauthorized":"$undefined"}]]}]
7:["$","$1","c",{"children":[null,["$","$L10",null,{"parallelRouterKey":"children","error":"$undefined","errorStyles":"$undefined","errorScripts":"$undefined","template":["$","$L12",null,{}],"templateStyles":"$undefined","templateScripts":"$undefined","notFound":"$undefined","forbidden":"$undefined","unauthorized":"$undefined"}]]}]
8:["$","$1","c",{"children":[["$","$L18",null,{"Component":"$19","serverProvidedParams":{"searchParams":{},"params":"$4:props:children:1:props:serverProvidedParams:params","promises":["$@1a","$@1b"]}}],[["$","script","script-0",{"src":"/_next/static/chunks/2_e3h_9j5rs50.js","async":true,"nonce":"$undefined"}],["$","script","script-1",{"src":"/_next/static/chunks/176itjfm4mv-a.js","async":true,"nonce":"$undefined"}],["$","script","script-2",{"src":"/_next/static/chunks/33frx3zlrv3f8.js","async":true,"nonce":"$undefined"}]],["$","$L1c",null,{"children":["$","$1d",null,{"name":"Next.MetadataOutlet","children":"$@1e"}]}]]}]
1f:[]
9:"$W1f"
a:["$","$1","h",{"children":[null,["$","$L20",null,{"children":"$L21"}],["$","div",null,{"hidden":true,"children":["$","$L22",null,{"children":["$","$1d",null,{"name":"Next.Metadata","children":"$L23"}]}]}],["$","meta",null,{"name":"next-size-adjust","content":""}]]}]
c:["$","link","0",{"rel":"stylesheet","href":"/_next/static/chunks/4384baud_6ozc.css","precedence":"next","crossOrigin":"$undefined","nonce":"$undefined"}]
d:["$","link","1",{"rel":"stylesheet","href":"/_next/static/chunks/2q72uqdvdxfu5.css","precedence":"next","crossOrigin":"$undefined","nonce":"$undefined"}]
17:"$4:props:children:1:props:serverProvidedParams:params"
1a:{}
1b:"$4:props:children:1:props:serverProvidedParams:params"
21:[["$","meta","0",{"charSet":"utf-8"}],["$","meta","1",{"name":"viewport","content":"width=device-width, initial-scale=1"}]]
24:I[27201,["/_next/static/chunks/3mi1ad2dacb47.js","/_next/static/chunks/0wd_oeyklqnyx.js","/_next/static/chunks/1ntn7efqc-iiw.js","/_next/static/chunks/04u9x8_fzw9b5.js","/_next/static/chunks/35ifdzvyefy82.js"],"IconMark"]
1e:null
23:[["$","title","0",{"children":"Console"}],["$","meta","1",{"name":"description","content":"Authorized operations console"}],["$","link","2",{"rel":"icon","href":"/favicon.ico?favicon.3zwyx4r7xzlza.ico","sizes":"256x256","type":"image/x-icon"}],["$","link","3",{"rel":"icon","href":"/icon.png?icon.3dd5jbyzohzmq.png","sizes":"1920x1920","type":"image/png"}],["$","$L24","4",{}]]
