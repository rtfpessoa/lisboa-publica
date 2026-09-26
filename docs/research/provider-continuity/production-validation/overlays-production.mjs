import {chromium,expect} from '/Users/rodrigo.fernandes/dev/lisboapublica/frontend/node_modules/@playwright/test/index.mjs';
const browser=await chromium.launch({headless:true,args:['--enable-unsafe-swiftshader']});
const results=[];
try{
for(const width of [1280,390]){
 const page=await browser.newPage({viewport:{width,height:800}}),errors=[];
 page.on('pageerror',e=>errors.push(e.message));
 await page.addInitScript(()=>{window.networkFeatures=[];const original=Worker.prototype.postMessage;Worker.prototype.postMessage=function(message,...args){if(message?.data?.source==='network'&&message?.data?.data?.features)window.networkFeatures=message.data.data.features;return Reflect.apply(original,this,[message,...args]);}});
 await page.goto('https://lisboapublica.rtfpessoa.xyz/');
 const names=['Metro de Lisboa','CP','Fertagus','TTSL'];
 if(width<760)await page.getByRole('button',{name:'Abrir pesquisa'}).click();
 await expect(page.getByRole('button',{name:names[0],exact:true})).toHaveAttribute('aria-pressed','true');
 for(const name of names.slice(1)){
  await expect(page.getByRole('button',{name,exact:true})).toHaveAttribute('aria-pressed','false');
  await page.getByRole('button',{name,exact:true}).click();
 }
 if(width<760)await page.getByRole('button',{name:'Fechar operadores'}).click();
 await page.getByRole('button',{name:'Camadas do mapa'}).click();
 for(const name of ['Linhas de metro','Percursos de autocarro','Linhas de comboio','Percursos fluviais'])await expect(page.getByRole('checkbox',{name})).toBeChecked();
 const modes=()=>page.evaluate(()=>window.networkFeatures.reduce((m,f)=>{m[f.properties.mode]=(m[f.properties.mode]??0)+1;return m;},{}));
 await expect.poll(async()=>Object.keys(await modes()).sort(),{timeout:20000}).toEqual(['ferry','metro','train']);
 const initial=await modes();expect(initial.train).toBeGreaterThan(0);expect(initial.ferry).toBeGreaterThan(0);
 await expect(page.locator('.layer-panel')).toContainText('CP:');
 await page.getByRole('checkbox',{name:'Linhas de comboio'}).uncheck();await expect.poll(async()=>Object.keys(await modes()).sort()).toEqual(['ferry','metro']);
 await page.getByRole('checkbox',{name:'Percursos fluviais'}).uncheck();await expect.poll(async()=>Object.keys(await modes()).sort()).toEqual(['metro']);
 await page.getByRole('checkbox',{name:'Linhas de comboio'}).check();await expect.poll(async()=>Object.keys(await modes()).sort()).toEqual(['metro','train']);
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=window.innerWidth)).toBe(true);
 expect(errors).toEqual([]);results.push({width,initial_network_features:initial,independent_toggle_and_selection:true,page_errors:errors});await page.close();
}
console.log(JSON.stringify(results,null,2));
}finally{await browser.close();}
