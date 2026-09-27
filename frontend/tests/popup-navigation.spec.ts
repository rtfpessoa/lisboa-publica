import {test,expect,type Page} from '@playwright/test';
import {separateMapTargets,type MapTarget} from '../src/mapTargets';
import {observationAge,routeName,sameVehicleService} from '../src/data';
import type {Vehicle} from '../src/api';

async function fixture(page:Page,operator='cp',mode='train',count=1){
 const epoch=Date.now(),iso=(n:number)=>new Date(n).toISOString(),day='2026-09-27';
 const reference={vehicle_id:`${operator}:v`,reference:'frozen-service'};
 const stop={id:`${operator}:station`,source_id:'station',operator_id:operator,name:'Lisboa Oriente',lat:38.731,lon:-9.145,route_ids:[]};
 const next={...stop,id:`${operator}:next`,source_id:'next',name:'Alcântara',lat:38.751,lon:-9.165};
 const v={id:reference.vehicle_id,source_id:'public-unit',operator_id:operator,plan_id:'plan',trip_id:`${operator}:trip`,operational_date:day,route_name:operator==='cp'?'IC':operator==='metro'?'Vm':'100',service_label:'18456',vehicle_ref:reference,position_kind:operator==='metro'?'estimated':'reported',current_status:'STOPPED_AT',stop_id:stop.id,stop_name:stop.name,lat:stop.lat,lon:stop.lon,observed_at:iso(epoch-360000),collected_at:iso(epoch),last_known:true,stale:true,source_url:'https://go.tmlmobilidade.pt/',speed_kmh:null,seated_capacity:0,contactless:false};
 const paged=(data:unknown[])=>({data,page:{limit:500,offset:0,total:data.length,has_more:false,revision:'fixture'}});
 let failure=false,delay=false,serviceChanged=false,stationFailure=false,networkChanged=false,noPlan=false;const requests:{path:string,query:URLSearchParams}[]=[];
 await page.addInitScript(()=>{const original=Worker.prototype.postMessage;(window as any).features={};Worker.prototype.postMessage=function(message,...args){if(message?.data?.data?.features)(window as any).features[message.data.source]=message.data.data.features;return Reflect.apply(original,this,[message,...args])}});
 await page.route('https://tiles.openfreemap.org/styles/positron',r=>r.fulfill({json:{version:8,sources:{},layers:[{id:'background',type:'background',paint:{'background-color':'#fff'}}]}}));
 await page.route('**/api/v1/**',async r=>{
  const u=new URL(r.request().url()),path=u.pathname,q=u.searchParams;requests.push({path,query:q});let data:unknown=paged([]);
  if(path.endsWith('/config'))data={dev_auth:false,live_refresh_seconds:5,history_retention_days:30,history_resolution_seconds:300};
  if(path.endsWith('/operators'))data=paged([{id:'metro',name:'Metro de Lisboa',mode:'metro',color:'#e22'},{id:operator,name:operator,mode,color:'#278044'}].filter((o,i,a)=>a.findIndex(v=>v.id===o.id)===i).map(o=>({...o,status:'ok',static_status:'ok',live_updated_at:iso(epoch),static_updated_at:iso(epoch),plan_id:noPlan?undefined:networkChanged?'replacement':'plan',reported_positions:0,estimated_positions:0})));
  if(path.endsWith('/stops'))data=paged([stop,networkChanged?{...next,name:'Nova estação'}:next].filter(s=>!q.get('q')||(s.name+' '+s.source_id).toLowerCase().includes(q.get('q')!.toLowerCase())));
  if(path.endsWith('/vehicles')&&stationFailure&&q.has('stop_id'))return r.fulfill({status:503,json:{message:'Fonte temporariamente indisponível'}});
  if(path.endsWith('/vehicles'))data=paged(Array.from({length:count},(_,i)=>({...v,id:i?`${operator}:v${i}`:v.id,vehicle_ref:i?{...v.vehicle_ref,vehicle_id:`${operator}:v${i}`} :v.vehicle_ref})));
  if(path.endsWith('/route-shapes'))data={...paged([]),coverage:[]};
  if(path.endsWith('/arrivals'))data=paged([{id:'arrival',operator_id:operator,stop_id:stop.id,route_id:`${operator}:route`,trip_id:'dated-trip',headsign:'Lisboa Oriente',service_label:'18456',kind:'scheduled',scheduled_at:iso(epoch+600000),vehicle_ref:reference}]);
  if(path.endsWith('/cp/predictions'))data={...paged([]),availability:{status:'ok',message:'Sem previsões verificadas'}};
  if(path.endsWith('/calls')){
   if(delay)await new Promise(resolve=>setTimeout(resolve,800));
   if(failure)return r.fulfill({status:410,json:{message:'Ligação expirada; atualize a origem.'}}).catch(()=>{});
   data={vehicle:serviceChanged?{...v,trip_id:'different-service'}:v,...paged(v.stop_id===next.id?[]:[{id:'visit',stop_id:next.id,stop_name:next.name,stop:next,kind:'scheduled',scheduled_at:iso(epoch+600000),source_url:v.source_url,stop_plan_id:noPlan?undefined:'plan',stop_static_updated_at:iso(epoch)}]),availability:'partial',coverage:'regional_subset',progress:'unknown'};
  }
  await r.fulfill({json:data}).catch(()=>{});
 });
 return {v,stop,next,requests,fail:()=>failure=true,delay:()=>delay=true,changeService:()=>serviceChanged=true,noPlan:()=>{noPlan=true;Object.assign(v,{plan_id:undefined,operational_date:undefined})},replaceNetwork:()=>networkChanged=true,stationFail:()=>stationFailure=true,current:(status=true)=>Object.assign(v,{observed_at:iso(epoch),last_known:false,stale:false,current_status:status?'STOPPED_AT':null}),move:()=>Object.assign(v,{observed_at:iso(epoch+6000),stop_id:next.id,stop_name:next.name,last_known:false,stale:false,vehicle_ref:{...reference,reference:'updated-observation'}})};
}
async function chooseStation(page:Page){await page.getByLabel('Pesquisar carreira ou paragem').fill('Oriente');await page.locator('.search-results button').last().click()}
async function selectProvider(page:Page,operator:string){if(operator==='metro')return;if(operator==='cm')await page.getByRole('button',{name:'Carris Metropolitana',exact:true}).click();else if(operator==='carris')await page.locator('.main-operator').first().click();else await page.getByRole('button',{name:operator==='cp'?'CP':'TTSL',exact:true}).click();await page.getByRole('button',{name:'Metro de Lisboa',exact:true}).click()}

for(const [operator,mode] of [['cp','train'],['metro','metro'],['ttsl','ferry'],['carris','bus']])test(`${mode} station to vehicle to next stop uses frozen references`,async({page})=>{
 const f=await fixture(page,operator,mode);await page.goto('/');await selectProvider(page,operator);await chooseStation(page);
 await page.locator('.station-vehicles button').first().click();await expect(page.locator('.vehicle-heading h3')).toContainText('18456');await expect(page.locator('.vehicle-calls')).toContainText('Alcântara');
 const panel=page.locator('.detail-panel');await expect(panel).not.toContainText('Indisponível');await expect(panel.locator('.specifications')).toContainText('0');await expect(panel.locator('.specifications')).toContainText('Não indicado pela fonte');await expect(panel).toContainText('há 6 min');
 if(operator==='cp')await page.screenshot({path:'../docs/research/popup-map-navigation/validation/popup-desktop.png'});
 await page.getByRole('button',{name:/Ver \d+ avisos/}).click();await expect(page.getByRole('region',{name:'Avisos e fontes'})).toBeFocused();
 await page.locator('.stop-link').click();await expect(panel.locator('h3')).toContainText('Alcântara');expect(f.requests.filter(r=>r.path.endsWith('/calls')).every(r=>r.query.get('reference')==='frozen-service')).toBe(true);
 await page.keyboard.press('Escape');await expect(panel).toHaveCount(0);
});
for(const dpr of [1,2])test(`overlapping station and train have independent touch targets at DPR ${dpr}`,async({browser})=>{
 const context=await browser.newContext({viewport:{width:390,height:800},deviceScaleFactor:dpr});const page=await context.newPage();const renderWarnings:string[]=[];page.on('console',m=>{if(m.type()==='warning'&&m.text().includes('layers[vehicle-points]'))renderWarnings.push(m.text())});await fixture(page);await page.goto('/');await page.getByRole('button',{name:'Abrir operadores'}).click();await selectProvider(page,'cp');await page.getByRole('button',{name:'Fechar operadores'}).click();
 await expect.poll(async()=>{const box=await page.locator('.sidebar').boundingBox();return box ? box.x+box.width : 0}).toBeLessThanOrEqual(0);
 await expect.poll(()=>page.evaluate(()=>(window as any).features.vehicles?.[0]?.properties.offsetX??0)).toBeGreaterThan(0);
 const features=await page.evaluate(()=>(window as any).features);expect(features.vehicles[0].geometry.coordinates).toEqual([-9.145,38.731]);expect(features['vehicle-links']).toHaveLength(1);
 const box=(await page.locator('.map canvas').boundingBox())!;const offset=[features.vehicles[0].properties.offsetX,features.vehicles[0].properties.offsetY];
 // Check the rendered glyph, not only worker input or the independent hit grid.
 await expect.poll(async()=>{
  const png=await page.screenshot();return page.evaluate(async({url,x,y,dpr})=>{const image=new Image();image.src=url;await image.decode();const canvas=document.createElement('canvas');canvas.width=image.width;canvas.height=image.height;const ctx=canvas.getContext('2d')!;ctx.drawImage(image,0,0);const pixels=ctx.getImageData(Math.round((x-20)*dpr),Math.round((y-20)*dpr),40*dpr,40*dpr).data;let green=0;for(let i=0;i<pixels.length;i+=4)if(pixels[i+1]>pixels[i]+8&&pixels[i+1]>pixels[i+2]+8)green++;return green},{url:`data:image/png;base64,${png.toString('base64')}`,x:box.x+box.width/2+offset[0]*.7,y:box.y+box.height/2+offset[1]*.7,dpr});
 }).toBeGreaterThan(20*dpr*dpr);
 expect(renderWarnings).toEqual([]);await expect(page.locator('.map-notice')).toHaveCount(0);
 await page.screenshot({path:`../docs/research/popup-map-navigation/validation/overlap-dpr${dpr}.png`});
 await page.mouse.click(box.x+box.width/2,box.y+box.height/2);await expect(page.locator('.detail-panel h3')).toContainText('Lisboa Oriente');await page.getByRole('button',{name:'Fechar detalhes'}).click();
 await page.mouse.click(box.x+box.width/2+offset[0]*.7,box.y+box.height/2+offset[1]*.7);await expect(page.locator('.vehicle-heading h3')).toContainText('Comboio 18456');
 if(dpr===2)await page.screenshot({path:'../docs/research/popup-map-navigation/validation/popup-mobile.png'});
 await page.getByRole('button',{name:'Fechar detalhes'}).click();await page.getByRole('button',{name:'Camadas do mapa'}).click();await page.getByRole('checkbox',{name:'Paragens'}).uncheck();await expect.poll(()=>page.evaluate(()=>{const p=(window as any).features.vehicles?.[0]?.properties;return [p?.offsetX,p?.offsetY]})).toEqual([0,0]);await context.close();
});
test('expired or changed station link keeps origin and never jumps to latest ID',async({page})=>{
 const f=await fixture(page);await page.goto('/');await selectProvider(page,'cp');await chooseStation(page);f.fail();await page.locator('.station-vehicles button').first().click();await expect(page.getByRole('alert')).toContainText('Ligação expirada');await expect(page.locator('.detail-panel h3')).toContainText('Lisboa Oriente');expect(f.requests.filter(r=>r.path.endsWith('/calls')).every(r=>r.query.has('reference'))).toBe(true);
});
test('closing origin cancels delayed navigation without reopening details',async({page})=>{
 const f=await fixture(page);await page.goto('/');await selectProvider(page,'cp');await chooseStation(page);f.delay();await page.locator('.station-vehicles button').first().click();await expect(page.getByRole('status').filter({hasText:'A abrir veículo'})).toBeVisible();await page.getByRole('button',{name:'Fechar detalhes'}).click();await page.waitForTimeout(1000);await expect(page.locator('.detail-panel')).toHaveCount(0);
});
test('presentation collisions preserve all dense targets and original positions',()=>{
 const stop:MapTarget={id:'station',kind:'stop',x:100,y:100},vehicles=Array.from({length:20},(_,i)=>({id:`v${i}`,kind:'vehicle' as const,x:100,y:100}));
 const layout=separateMapTargets(vehicles,[stop]);expect(layout.offsets.size).toBeLessThanOrEqual(8);const hits=layout.hit(100,100);expect(hits.some(h=>h.kind==='stop')).toBe(true);expect(hits.length).toBeGreaterThan(1);expect(hits.length+layout.offsets.size).toBe(21);expect(vehicles.every(v=>v.x===100&&v.y===100)).toBe(true);
 for(const offset of layout.offsets.values())expect(Math.hypot(...offset)).toBeGreaterThanOrEqual(48);
});
test('names, original-clock age and service identity remain unambiguous',()=>{
 const at=Date.parse('2026-10-25T00:59:00Z');expect(observationAge('2026-10-25T00:59:00Z',at+60000)).toBe('há 1 min');expect(observationAge('2026-10-25T00:59:00Z',at+3600000)).toBe('há 1 h');expect(observationAge('invalid',at)).toBe('hora não confirmada');expect(observationAge(new Date(at+31000).toISOString(),at)).toBe('hora não confirmada');expect(routeName('cp','AP')).toBe('Alfa Pendular');expect(routeName('cp','R · Sintra')).toBe('Regional · Sintra');expect(routeName('metro','Vm')).toBe('Linha Vermelha');expect(routeName('metro','Am')).toBe('Linha Amarela');
 const v={id:'v',operator_id:'metro',plan_id:'plan',trip_id:'trip',operational_date:'2026-09-27'} as Vehicle;expect(sameVehicleService(v,{...v,trip_id:'other'})).toBe(false);expect(sameVehicleService(v,{...v,operational_date:undefined})).toBe(false);
});


test('same service new observation rebinds remaining calls without showing passed stops',async({page})=>{
 const f=await fixture(page);f.current();await page.clock.install();await page.goto('/');await selectProvider(page,'cp');await chooseStation(page);await page.locator('.station-vehicles button').first().click();await expect(page.locator('.vehicle-calls')).toContainText('Alcântara');f.move();await page.clock.fastForward(10000);
 await expect.poll(()=>f.requests.some(r=>r.path.endsWith('/calls')&&r.query.get('reference')==='updated-observation')).toBe(true);await expect(page.locator('.vehicle-calls .stop-link')).toHaveCount(0);await expect(page.locator('.vehicle-status')).toContainText('Parado em Alcântara');
});
test('frozen calls cannot navigate a stop replaced by a different static plan',async({page})=>{
 const f=await fixture(page);await page.goto('/');await selectProvider(page,'cp');await chooseStation(page);await page.locator('.station-vehicles button').first().click();await expect(page.locator('.stop-link')).toContainText('Alcântara');f.replaceNetwork();await page.locator('.stop-link').click();await expect(page.locator('.popup-footnotes')).toContainText('A rede publicada mudou');await expect(page.locator('.vehicle-heading')).toContainText('Comboio 18456');
});
for(const status of [true,false])test(`station failed refresh qualifies cached references and expires them (${status})`,async({page})=>{
 const f=await fixture(page);f.current(status);await page.clock.install();await page.goto('/');await selectProvider(page,'cp');await chooseStation(page);await expect(page.locator('.station-vehicles')).toContainText(status?'Parado em Lisboa Oriente':'Referência publicada');await expect(page.locator('.station-vehicles')).not.toContainText('Último registo');f.stationFail();await page.clock.fastForward(8000);await expect(page.locator('.station-vehicles')).toContainText('Último registo');await page.clock.fastForward(3600000);await expect(page.locator('.station-vehicles button.vehicle-link')).toHaveCount(0);
});
test('dense chooser heartbeat preserves a lower focused target',async({page})=>{
 await fixture(page,'cp','train',20);await page.clock.install();await page.goto('/');await selectProvider(page,'cp');await expect.poll(()=>page.evaluate(()=>(window as any).features.vehicles?.length)).toBe(20);await page.waitForTimeout(300);const box=(await page.locator('.map canvas').boundingBox())!;await page.mouse.click(box.x+box.width/2,box.y+box.height/2);const picker=page.locator('.map-target-picker');await expect(picker).toBeVisible();const lower=picker.locator('button').last();await lower.focus();await expect(lower).toBeFocused();const before=await picker.evaluate(el=>el.scrollTop);await page.clock.fastForward(6000);await expect(lower).toBeFocused();expect(await picker.evaluate(el=>el.scrollTop)).toBe(before);
});

test('CM published next stop without a plan remains safely clickable',async({page})=>{
 const f=await fixture(page,'cm','bus');f.noPlan();await page.goto('/');await selectProvider(page,'cm');await chooseStation(page);await page.locator('.station-vehicles button').first().click();await expect(page.locator('.stop-link')).toContainText('Alcântara');await page.locator('.stop-link').click();await expect(page.locator('.detail-panel h3')).toContainText('Alcântara');
});
