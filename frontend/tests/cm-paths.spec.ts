import {test,expect,type Page} from '@playwright/test';

async function fixture(page:Page){
 const at=new Date().toISOString();let delayed=false,fail=false,unmatched=false,completedGeometry=false,geometryFailure=false,abortedGeometry=0;let releaseGeometry=()=>{};const geometryWait=new Promise<void>(resolve=>releaseGeometry=resolve);
 const station={id:'cm:S',source_id:'S',operator_id:'cm',name:'Centro',lat:38.731,lon:-9.145,route_ids:['cm:1001']},terminal={id:'cm:T',source_id:'T',operator_id:'cm',name:'Terminal',lat:38.735,lon:-9.148,route_ids:['cm:1001']};
 const shape={id:'cm:plan/agency/1001_0/exact/0',operator_id:'cm',route_id:'cm:1001',shape_id:'exact',direction_id:0,plan_id:'plan',color:'#278044',source_url:'https://go.tmlmobilidade.pt/',updated_at:at,geometry:[[-9.145,38.731],[-9.148,38.735]]};
 const vehicle={id:'cm:v',source_id:'v',operator_id:'cm',pattern_id:'cm:[plan][agency]p',route_id:'cm:1001',route_name:'1001',position_kind:'reported',current_status:'STOPPED_AT',stop_id:station.id,stop_name:station.name,lat:38.731,lon:-9.145,observed_at:at,collected_at:at,source_url:'https://api.carrismetropolitana.pt/v2/vehicles',vehicle_ref:{vehicle_id:'cm:v',reference:'frozen-pattern'}};
 const requests:{path:string,query:URLSearchParams}[]=[];const paged=(data:unknown[])=>({data,page:{total:data.length,limit:500,offset:0,has_more:false,revision:'fixture'}});
 page.on('requestfailed',r=>{if(new URL(r.url()).searchParams.get('include_geometry')==='true')abortedGeometry++});
 await page.addInitScript(()=>{const original=Worker.prototype.postMessage;(window as any).features={};(window as any).posts={};Worker.prototype.postMessage=function(message,...args){if(message?.data?.data?.features){const key=message.data.source;(window as any).features[key]=message.data.data.features;(window as any).posts[key]=((window as any).posts[key]??0)+1}return Reflect.apply(original,this,[message,...args])}});
 await page.route('https://tiles.openfreemap.org/styles/positron',r=>r.fulfill({json:{version:8,sources:{},layers:[{id:'background',type:'background',paint:{'background-color':'#fff'}}]}}));
 await page.route('**/api/v1/**',async r=>{
  const u=new URL(r.request().url()),path=u.pathname,q=u.searchParams;requests.push({path,query:q});let data:unknown=paged([]);
  if(path.endsWith('/config'))data={dev_auth:false,live_refresh_seconds:5,history_retention_days:30,history_resolution_seconds:300};
  if(path.endsWith('/operators'))data=paged([{id:'metro',name:'Metro de Lisboa',mode:'metro',color:'#e22'},{id:'cm',name:'Carris Metropolitana',mode:'bus',color:'#278044'}].map(o=>({...o,status:'ok',static_status:'ok',live_updated_at:at,static_updated_at:at,reported_positions:1,estimated_positions:0})));
  if(path.endsWith('/stops'))data=paged([station,terminal].filter(s=>!q.get('q')||s.name.toLowerCase().includes(q.get('q')!.toLowerCase())||s.source_id===q.get('q')));
  if(path.endsWith('/vehicles'))data=paged(q.get('operators')?.includes('cm')?[vehicle]:[]);
  if(path.endsWith('/route-shapes'))data={...paged([{...shape,id:'other-variant',shape_id:'other',geometry:[[-9.13,38.70],[-9.12,38.71]]}]),coverage:[]};
  if(path.endsWith('/calls')){
   if(geometryFailure&&q.get('include_geometry')==='true')return r.fulfill({status:503,json:{message:'Geometria temporariamente indisponível'}});
   if(delayed&&q.get('include_geometry')==='true'){await geometryWait;completedGeometry=true}if(fail)return r.fulfill({status:410,json:{message:'Ligação expirada'}}).catch(()=>{});
   const offset=Number(q.get('offset')??0),limit=Number(q.get('limit')??20),rows=Array.from({length:25},(_,i)=>{const stop=i%2?terminal:station;return {id:stop.id+':'+i,stop_id:stop.id,stop_name:stop.name,stop,stop_sequence:i,kind:'published_route',source_url:shape.source_url,stop_static_updated_at:at}});
   if(unmatched){data={vehicle,data:rows.slice(0,1),page:{total:1,limit,offset:0,has_more:false,revision:'pinned-calls'},coverage:'regional_subset',progress:'unknown',availability:'next_stop_only'}}else data={vehicle,data:rows.slice(offset,offset+limit),page:{total:25,limit,offset,has_more:offset+limit<25,revision:'pinned-calls'},coverage:'complete_published_route',progress:'unknown',availability:'available',...(q.get('include_geometry')==='true'&&offset===0?{geometry:shape}:{})};
  }
  await r.fulfill({json:data}).catch(()=>{});
 });
 return {requests,delay:()=>delayed=true,release:()=>releaseGeometry(),finished:()=>completedGeometry,aborted:()=>abortedGeometry,failGeometry:()=>geometryFailure=true,recoverGeometry:()=>geometryFailure=false,unmatched:()=>unmatched=true,fail:()=>fail=true,shape};
}
async function openVehicle(page:Page,showPath=true){
 await page.goto('/');if(!showPath){await page.getByRole('button',{name:'Camadas do mapa'}).click();await page.getByRole('checkbox',{name:'Percursos de autocarro'}).uncheck();await page.getByRole('button',{name:'Fechar camadas'}).click()}if(await page.getByRole('button',{name:'Abrir operadores'}).isVisible())await page.getByRole('button',{name:'Abrir operadores'}).click();
 await page.getByRole('button',{name:'Carris Metropolitana',exact:true}).click();await page.getByRole('button',{name:'Metro de Lisboa',exact:true}).click();
 await page.getByLabel('Pesquisar carreira ou paragem').fill('Centro');await page.locator('.search-results button').last().click();await page.locator('.station-vehicles button').first().click();
}
async function highlight(page:Page){return page.evaluate(()=>(window as any).features['vehicle-path']??[])}
for(const mobile of [false,true])test(`selective complete CM path and exact map variant (${mobile?'mobile':'desktop'})`,async({page})=>{
 if(mobile)await page.setViewportSize({width:390,height:800});const f=await fixture(page);await page.goto('/');expect(f.requests.filter(r=>r.path.endsWith('/calls'))).toHaveLength(0);await openVehicle(page);
 await expect(page.locator('.vehicle-calls h4')).toHaveText('Percurso completo publicado');await expect(page.locator('.vehicle-calls .arrival')).toHaveCount(20);await expect(page.locator('.vehicle-calls')).toContainText('25 paragens');await expect.poll(()=>highlight(page)).toHaveLength(1);expect((await highlight(page))[0].geometry.coordinates).toEqual(f.shape.geometry);
 await expect(page.locator('.vehicle-calls')).not.toContainText('Horário planeado');await expect(page.locator('.vehicle-calls time')).toHaveCount(0);await expect(page.locator('.map-notice')).toHaveCount(0);await page.locator('.vehicle-calls').getByRole('button',{name:'Seguinte'}).click();await expect(page.locator('.vehicle-calls .arrival')).toHaveCount(5);await expect.poll(()=>highlight(page)).toHaveLength(1);
 const later=f.requests.find(r=>r.path.endsWith('/calls')&&r.query.get('offset')==='20');expect(later?.query.get('revision')).toBe('pinned-calls');expect(later?.query.get('include_geometry')).toBe('false');
 await page.getByRole('checkbox',{name:'Mostrar percursos de autocarro no mapa'}).uncheck();await expect.poll(()=>highlight(page)).toHaveLength(0);await page.getByRole('checkbox',{name:'Mostrar percursos de autocarro no mapa'}).check();await expect.poll(()=>highlight(page)).toHaveLength(1);await page.getByRole('button',{name:'Fechar detalhes'}).click();await expect.poll(()=>highlight(page)).toHaveLength(0);
 expect(f.requests.filter(r=>r.path.endsWith('/calls')&&r.query.get('include_geometry')==='true').every(r=>!r.query.get('offset')||r.query.get('offset')==='0')).toBe(true);
});
test('reenabling overlays on a later page obtains geometry from pinned page zero',async({page})=>{
 const f=await fixture(page);await page.goto('/');await page.getByRole('button',{name:'Camadas do mapa'}).click();await page.getByRole('checkbox',{name:'Percursos de autocarro'}).uncheck();await page.getByRole('button',{name:'Fechar camadas'}).click();
 // Keep the same page so the overlay preference is retained.
 await page.getByRole('button',{name:'Carris Metropolitana',exact:true}).click();await page.getByRole('button',{name:'Metro de Lisboa',exact:true}).click();await page.getByLabel('Pesquisar carreira ou paragem').fill('Centro');await page.locator('.search-results button').last().click();await page.locator('.station-vehicles button').first().click();
 await page.locator('.vehicle-calls').getByRole('button',{name:'Seguinte'}).click();await expect(page.locator('.vehicle-calls .arrival')).toHaveCount(5);
 await page.getByRole('checkbox',{name:'Mostrar percursos de autocarro no mapa'}).check();await expect.poll(()=>highlight(page)).toHaveLength(1);expect(f.requests.some(r=>r.path.endsWith('/calls')&&r.query.get('revision')==='pinned-calls'&&r.query.get('offset')==='0'&&r.query.get('include_geometry')==='true')).toBe(true);
});
test('closing cancels late geometry and provider/tab changes clear the highlight',async({page})=>{
 const f=await fixture(page);await openVehicle(page);await expect.poll(()=>highlight(page)).toHaveLength(1);await page.getByRole('button',{name:'Histórico',exact:true}).click();await expect.poll(()=>highlight(page)).toHaveLength(0);await page.getByRole('button',{name:'Tempo real',exact:true}).click();await expect.poll(()=>highlight(page)).toHaveLength(0);await page.getByRole('button',{name:'Carris Metropolitana',exact:true}).click();await expect.poll(()=>highlight(page)).toHaveLength(0);
 f.delay();await openVehicle(page);await expect(page.locator('.vehicle-heading')).toBeVisible();await expect(page.locator('.vehicle-calls')).toContainText('A carregar paragens');await page.getByRole('button',{name:'Fechar detalhes'}).click();f.release();await expect.poll(()=>f.finished()).toBe(true);await expect(page.locator('.detail-panel')).toHaveCount(0);await expect.poll(()=>highlight(page)).toHaveLength(0);
});
test('heartbeats do not repost the identical path',async({page})=>{
 await fixture(page);await page.clock.install();await openVehicle(page);await expect.poll(()=>highlight(page)).toHaveLength(1);const posts=await page.evaluate(()=>(window as any).posts['vehicle-path']);await page.clock.fastForward(16000);expect(await page.evaluate(()=>(window as any).posts['vehicle-path'])).toBe(posts);await expect.poll(()=>highlight(page)).toHaveLength(1);
});

test('unmatched patterns show published next stop without claiming a full route',async({page})=>{
 const f=await fixture(page);f.unmatched();await openVehicle(page);await expect(page.locator('.vehicle-calls h4')).toHaveText('Próxima paragem publicada');await expect(page.locator('.popup-footnotes')).toContainText('Percurso completo indisponível');await expect.poll(()=>highlight(page)).toHaveLength(0);await expect(page.locator('.vehicle-calls .arrival')).toHaveCount(1);
});

test('disabling overlays aborts pending later-page geometry and never retains its late result',async({page})=>{
 const f=await fixture(page);await openVehicle(page,false);await page.locator('.vehicle-calls').getByRole('button',{name:'Seguinte'}).click();await expect(page.locator('.vehicle-calls .arrival')).toHaveCount(5);f.delay();
 const toggle=page.getByRole('checkbox',{name:'Mostrar percursos de autocarro no mapa'});await toggle.check();await expect.poll(()=>f.requests.some(r=>r.path.endsWith('/calls')&&r.query.get('include_geometry')==='true'&&r.query.get('revision')==='pinned-calls')).toBe(true);await expect(page.locator('.vehicle-calls')).toHaveAttribute('aria-busy','true');await toggle.uncheck();await expect.poll(()=>f.aborted()).toBeGreaterThan(0);await expect.poll(()=>highlight(page)).toHaveLength(0);
 f.release();await expect.poll(()=>f.finished()).toBe(true);await expect.poll(()=>highlight(page)).toHaveLength(0);await toggle.check();await expect.poll(()=>highlight(page)).toHaveLength(1);expect(f.requests.filter(r=>r.path.endsWith('/calls')&&r.query.get('include_geometry')==='true')).toHaveLength(2);
});
test('valid first-page geometry recovers from a failed later-page lookup',async({page})=>{
 const f=await fixture(page);await openVehicle(page,false);await page.locator('.vehicle-calls').getByRole('button',{name:'Seguinte'}).click();await expect(page.locator('.vehicle-calls .arrival')).toHaveCount(5);f.failGeometry();await page.getByRole('checkbox',{name:'Mostrar percursos de autocarro no mapa'}).check();await expect(page.locator('.popup-footnotes')).toContainText('Percurso no mapa indisponível');await expect(page.getByRole('button',{name:'Atualizar percurso',exact:true})).toBeVisible();await expect.poll(()=>highlight(page)).toHaveLength(0);
 f.recoverGeometry();await page.locator('.vehicle-calls').getByRole('button',{name:'Anterior'}).click();await expect(page.locator('.vehicle-calls .arrival')).toHaveCount(20);await expect.poll(()=>highlight(page)).toHaveLength(1);await expect(page.locator('.popup-footnotes')).not.toContainText('Percurso no mapa indisponível');
});
