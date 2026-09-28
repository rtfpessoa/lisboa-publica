import {test,expect,type Page} from '@playwright/test';

async function fixture(page:Page,mode:'station'|'vehicle'='station'){
 const epoch=Date.now(),iso=(delta:number)=>new Date(epoch+delta).toISOString();
 const requests:URL[]=[];let expired=false,paused=false;
 const dirs=[{line_key:'red',line_name:'Linha Vermelha',color:'#e22',direction_key:'airport',label:'Aeroporto',count:26},{line_key:'red',line_name:'Linha Vermelha',color:'#e22',direction_key:'ss',label:'São Sebastião',count:0},{line_key:'yellow',line_name:'Linha Amarela',color:'#dba800',direction_key:'od',label:'Odivelas',count:1},{line_key:'yellow',line_name:'Linha Amarela',color:'#dba800',direction_key:'rato',label:'Rato',count:0},{line_key:'red',line_name:'Linha Vermelha',color:'#e22',direction_key:null,label:'Sentido não identificado',count:1}];
 const pageFor=(data:unknown[],offset=0,total=data.length,limit=25)=>({data,page:{offset,total,limit,has_more:offset+data.length<total,revision:'fixture-revision'}});
 const evidence=(delta:number)=>({at:iso(delta),source_url:'https://official.example',source_updated_at:iso(0),collected_at:iso(0),valid_until:iso(90000)});
 const absent={kind:'unavailable',at:null,reason:'Sem registo real',actual:null,prediction:null,schedule:null};
 const future={kind:'schedule',at:iso(600000),reason:'',actual:null,prediction:null,schedule:evidence(600000)};
 const coverage=()=>({status:'partial',message:'Tempos reais anteriores sem fonte comprovada.',actual_arrivals:false,actual_departures:false,history_collection_status:paused?'paused':'collecting',source_updated_at:iso(0)});
 const stop={id:'carris:S',operator_id:'carris',source_id:'S',name:'Alameda',lat:38.731,lon:-9.145,route_ids:['red']};
 await page.route('https://tiles.openfreemap.org/styles/positron',r=>r.fulfill({json:{version:8,sources:{},layers:[{id:'background',type:'background',paint:{'background-color':'#fff'}}]}}));
 await page.route('**/api/v1/**',async r=>{
  const u=new URL(r.request().url()),p=u.pathname;requests.push(u);let json:unknown=pageFor([]);
  if(p.endsWith('/config'))json={dev_auth:false,live_refresh_seconds:5,history_retention_days:30,history_resolution_seconds:300};
  if(p.endsWith('/operators'))json=pageFor([{id:'carris',name:'Carris',mode:'bus',color:'#e22',status:'ok',static_status:'ok',live_updated_at:iso(0),estimated_positions:1,reported_positions:1}]);
  if(p.endsWith('/metrics'))json={speed_kmh:null};
  if(p.endsWith('/route-shapes'))json={...pageFor([]),coverage:[]};
  if(p.endsWith('/metro/status'))json={status:'unconfigured',message:'Fonte direta indisponível',lines:[]};
  if(p.endsWith('/stops'))json=pageFor(mode==='station'?[stop]:[]);
  if(p.endsWith('/vehicles'))json=pageFor(mode==='vehicle'?[{id:'carris:v',source_id:'v',operator_id:'carris',route_name:'Linha Vermelha',position_kind:'reported',lat:38.731,lon:-9.145,observed_at:iso(0),collected_at:iso(0),source_url:'https://official.example',stale:false,last_known:false,inactive_at:iso(300000),last_known_expires_at:iso(3600000)}]:[]);
  if(p.endsWith('/board'))json={directions:dirs,coverage:coverage(),revision:paused?'paused-revision':'fixture-revision'};
  if(p.endsWith('/board/calls')){
   if(expired){expired=false;return r.fulfill({status:410,json:{message:'A coleção mudou'}})}
   const direction=u.searchParams.get('direction_key'),offset=Number(u.searchParams.get('offset')??0),total=direction==='airport'?26:direction==='od'||direction==='unknown'?1:0;
   const data=Array.from({length:Math.min(25,total-offset)},(_,n)=>({id:direction+':'+(offset+n),journey_id:null,line_key:u.searchParams.get('line_key'),direction_key:direction==='unknown'?null:direction,stop_id:stop.id,stop_name:stop.name,stop_sequence:n,destination:direction==='od'?'Campo Grande':direction==='airport'?'Aeroporto':'Destino publicado',arrival:future,departure:absent,phase:'future'}));
   json={...pageFor(data,offset,total),coverage:coverage()};
  }
  if(p.endsWith('/journey')){
   const offset=Number(u.searchParams.get('offset')??300),total=601;
   const data=Array.from({length:Math.min(100,total-offset)},(_,n)=>{const i=offset+n;return {id:'visit:'+i,journey_id:'journey',line_key:'red',direction_key:'airport',stop_id:'carris:S'+(i%10),stop_name:i===600?'Porto · fora da área do mapa':'Estação '+(i%10),stop_sequence:i+1,destination:'Aeroporto',arrival:i<300?absent:i===300?{...absent,kind:'actual',at:iso(-30000),reason:'',actual:evidence(-30000)}:future,departure:i<300?absent:future,phase:i<300?'previous':i===300?'current':'future'}});
   json={association:'resolved',message:'',journey_id:'journey',line_name:'Linha Vermelha',direction:'Aeroporto',destination:'Aeroporto',progress:'estimated',next_index:300,complete:true,...pageFor(data,offset,total,100),coverage:coverage()};
  }
  await r.fulfill({json});
 });
 return {requests,setExpired:()=>{expired=true},setPaused:()=>{paused=true}};
}
async function selectCarris(page:Page,close=false){if((page.viewportSize()?.width??1280)<760)await page.getByRole('button',{name:'Abrir operadores'}).click();await page.locator('.main-operator').filter({hasText:'Dashboard'}).click();await page.getByRole('button',{name:'Metro de Lisboa',exact:true}).click();if(close&&(page.viewportSize()?.width??1280)<760)await page.getByRole('button',{name:'Fechar operadores'}).click()}
async function openStation(page:Page){await selectCarris(page);await page.getByLabel('Pesquisar carreira ou paragem').fill('alameda');await page.locator('.search-results button').last().click();await expect(page.locator('.direction-matrix')).toBeVisible()}
for(const width of [1280,390])test(`station directions, header, short destination and pagination at ${width}px`,async({page})=>{
 await page.setViewportSize({width,height:850});const f=await fixture(page);await page.goto('/');await openStation(page);
 const panel=page.locator('.detail-panel');await panel.getByRole('button',{name:/Aeroporto/}).click();
 await expect(panel.locator('.direction-detail h4')).toHaveText('Próximas chegadas e partidas → Aeroporto');
 await expect(panel.locator('.transit-call:not(.call-head)')).toHaveCount(25);
 await expect(panel.locator('.transit-call:not(.call-head)')).not.toContainText(['Destino: Aeroporto']);
 await panel.getByRole('button',{name:'Seguinte',exact:true}).click();await expect(panel.locator('.transit-call:not(.call-head)')).toHaveCount(1);
 expect(f.requests.some(u=>u.pathname.endsWith('/board/calls')&&u.searchParams.get('offset')==='25'&&u.searchParams.get('revision')==='fixture-revision')).toBe(true);
 await panel.getByRole('button',{name:/São Sebastião/}).click();await expect(panel.locator('.direction-detail h4')).toContainText('São Sebastião');await expect(panel).toContainText('Sem resultados disponíveis para este sentido nas próximas duas horas');
 await panel.getByRole('button',{name:/Odivelas/}).click();await expect(panel.locator('.direction-detail h4')).toContainText('Odivelas');await expect(panel.locator('.transit-call:not(.call-head)')).toContainText('Destino: Campo Grande');
 await panel.getByRole('button',{name:/Sentido não identificado/}).click();await expect(panel.locator('.direction-detail h4')).toContainText('Sentido não identificado');
 await expect.poll(()=>page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
});
for(const width of [1280,390])test(`vehicle complete timeline, repeated visits and independent times at ${width}px`,async({page})=>{
 await page.setViewportSize({width,height:850});const f=await fixture(page,'vehicle');await page.goto('/');await selectCarris(page,true);await expect(page.locator('.map')).toHaveAttribute('aria-busy','false');await page.waitForTimeout(300);const box=await page.locator('.map canvas').boundingBox();await page.mouse.click(box!.x+box!.width/2,box!.y+box!.height/2);
 const panel=page.locator('.detail-panel');await expect(panel).toContainText('Percurso completo · 601 visitas');await expect(panel.locator('.visit-next')).toContainText('Real');await expect(panel.locator('.visit-next')).toContainText('Horário');await expect(panel).toContainText('Progresso estimado');
 await panel.getByRole('button',{name:'Ver início',exact:true}).click();await expect(panel.locator('.pagination')).toContainText('1–100 de 601');await expect(panel.locator('.visit-previous')).toHaveCount(100);await expect(panel.locator('.visit-previous time')).toHaveCount(0);
 await panel.getByRole('button',{name:'Ver próxima paragem',exact:true}).click();await expect(panel.locator('.pagination')).toContainText('301–400 de 601');
 for(let n=0;n<3;n++)await panel.getByRole('button',{name:'Seguinte',exact:true}).click();await expect(panel).toContainText('Porto · fora da área do mapa');await expect(panel.locator('.pagination')).toContainText('601–601 de 601');
 expect(f.requests.filter(u=>u.pathname.endsWith('/journey')).length).toBeLessThan(15);
 await expect.poll(()=>page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
});
test('expired station revision preserves selection and storage pause is explicit',async({page})=>{
 const f=await fixture(page);await page.goto('/');await openStation(page);await page.locator('.detail-panel').getByRole('button',{name:/Aeroporto/}).click();f.setExpired();await page.getByRole('button',{name:'Seguinte',exact:true}).click();await expect(page.locator('.direction-detail h4')).toContainText('Aeroporto');await expect(page.locator('.pagination')).toContainText('26 resultados');await expect(page.locator('.direction-detail .transit-call:not(.call-head)')).toHaveCount(1);expect(f.requests.filter(u=>u.pathname.endsWith('/board/calls')).at(-1)?.searchParams.get('offset')).toBe('25');f.setPaused();await expect(page.locator('.detail-panel')).toContainText('Recolha de tempos reais em pausa',{timeout:15000});
});
