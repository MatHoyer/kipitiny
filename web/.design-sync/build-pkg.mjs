// Builds the local homelab-ui package: prop types with tsc, then src/index.css with Tailwind for the sync: fonts are shipped separately
// (latin only, see fonts/), and common utilities are kept so designs can lay out with them.
import { execFileSync } from "node:child_process";
import { copyFileSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import tailwindcss from "@tailwindcss/vite";
import { build } from "vite";

execFileSync("node_modules/.bin/tsc", ["-p", ".design-sync/pkg/tsconfig.json"], { stdio: "inherit" });

const css = readFileSync("src/index.css", "utf8").replace(/^@import "@fontsource.*\n/gm, "");
const safelist = `
@source "../../../src/components/ui";
@source "../../previews";
@source inline("{flex,inline-flex,grid,block,inline-block,hidden,flex-col,flex-row,flex-wrap,flex-1,items-{start,center,end,stretch},justify-{start,center,end,between},self-{start,center,end},relative,absolute,sticky,overflow-hidden,overflow-auto,w-full,h-full,min-h-svh,min-w-0,mx-auto,truncate,shrink-0,grow,text-left,text-center,text-right,font-sans,font-mono,font-display,font-normal,font-medium,font-semibold,font-bold,text-xs,text-sm,text-base,text-lg,text-xl,text-2xl,text-3xl,text-4xl,tracking-tight,border,border-t,border-b,border-l,border-r,rounded-sm,rounded-md,rounded-lg,rounded-xl,rounded-2xl,rounded-full,shadow-sm,shadow-md}");
@source inline("{gap,gap-x,gap-y,space-y,space-x,p,px,py,pt,pb,pl,pr,m,mx,my,mt,mb,ml,mr}-{0,0.5,1,1.5,2,3,4,5,6,8,10,12,16}");
@source inline("{w,h,size}-{4,5,6,8,10,12,16,24,32,48,64}");
@source inline("max-w-{xs,sm,md,lg,xl,2xl,3xl,4xl,5xl,6xl}");
@source inline("grid-cols-{1,2,3,4,5,6}");
@source inline("{sm:,md:,lg:,}{grid-cols-1,grid-cols-2,grid-cols-3,grid-cols-4,flex-row,flex-col,hidden,block,flex}");
@source inline("{bg,text,border,ring}-{background,foreground,card,card-foreground,popover,popover-foreground,primary,primary-foreground,secondary,secondary-foreground,muted,muted-foreground,accent,accent-foreground,destructive,border,input,ring}");
@source inline("{bg-muted/40,bg-muted/50,bg-primary/80,bg-destructive/10,bg-destructive/20,ring-1,ring-foreground/10,hover:bg-muted,hover:bg-accent,hover:text-foreground,transition-colors}");
`;
mkdirSync(".design-sync/.cache/css", { recursive: true });
const input = ".design-sync/.cache/css/ds.css";
writeFileSync(input, css + safelist);
await build({
  configFile: false,
  logLevel: "warn",
  publicDir: false,
  plugins: [tailwindcss()],
  build: {
    outDir: ".design-sync/.cache/css/out",
    emptyOutDir: true,
    cssMinify: false,
    rollupOptions: { input, output: { assetFileNames: "ds[extname]" } },
  },
});
copyFileSync(".design-sync/.cache/css/out/ds.css", ".design-sync/pkg/dist/ds.css");
