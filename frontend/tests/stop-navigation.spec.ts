import {test,expect,type Page} from '@playwright/test';
import {canonicalStop,canonicalStops} from '../src/stationIdentity';
import type {Stop} from '../src/api';

test('station grouping requires a complete same-operator hierarchy',()=>{
 const stop=(id:string,parent_id:string|null=null)=>({id,operator_id:id.split(':')[0],parent_id,name:'Same published name',lat:38.731,lon:-9.145} as Stop);
 const parent=stop('metro:M'),child=stop('metro:P','metro:M'),bus=stop('cm:A');
 expect(canonicalStops([child,parent,bus]).map(s=>s.id)).toEqual(['metro:M','cm:A']);
 expect(canonicalStop(child,[]).id).toBe('metro:P');
 const cross=stop('cm:B','metro:M');expect(canonicalStop(cross,[parent]).id).toBe('cm:B');
 const a=stop('metro:A','metro:B'),b=stop('metro:B','metro:A');
 expect(canonicalStops([a,b]).map(s=>s.id)).toEqual(['metro:A','metro:B']);
 expect(canonicalStop(stop('metro:self','metro:self'),[]).id).toBe('metro:self');
});
async function fixture(page:Page,platforms=false){
 const epoch=Date.now(),now=new Date(epoch).toISOString(),requests:string[]=[];let failure=false,expectedDelta=300000;
 const operators=['cm','metro'].map(id=>({id,name:id==='cm'?'Carris Metropolitana':'Metro de Lisboa',mode:id==='metro'?'metro':'bus',color:'#f5b800',status:'ok',static_status:'ok',live_updated_at:now,static_updated_at:now,reported_positions:0,estimated_positions:0,error:null,note:''}));
 const stops=[{id:'cm:A',name:'Paragem Alfa',lat:38.731,lon:-9.145},{id:'cm:B',name:'Paragem Beta',lat:38.73136,lon:-9.145},{id:'cm:C',name:'Paragem Gama',lat:38.73172,lon:-9.145},{id:'metro:M',name:'Metro oculto',lat:38.731,lon:-9.145}].map(s=>({...s,source_id:s.id,operator_id:s.id.split(':')[0],route_ids:[]}));
 const catalogue=platforms?[...stops,...Array.from({length:4},(_,n)=>({...stops[3],id:`metro:P${n}`,source_id:`P${n}`,parent_id:'metro:M'}))]:stops;
 const paged=(data:unknown[])=>({data,page:{limit:500,offset:0,total:data.length,has_more:false,revision:'fixture'}});
 await page.route('https://tiles.openfreemap.org/styles/positron',r=>r.fulfill({json:{version:8,sources:{},layers:[{id:'bg',type:'background',paint:{'background-color':'#fff'}}]}}));
 await page.route('**/api/v1/**',async r=>{
  const u=new URL(r.request().url());let data:unknown=paged([]);
  if(u.pathname.endsWith('/operators'))data=paged(operators);
  if(u.pathname.endsWith('/health'))data={status:'ok'};
  if(u.pathname.endsWith('/config'))data={dev_auth:false,live_refresh_seconds:5,history_retention_days:30,history_resolution_seconds:300};
  if(u.pathname.endsWith('/metrics'))data={speed_kmh:null};
  if(u.pathname.endsWith('/route-shapes'))data={...paged([]),coverage:[]};
  if(u.pathname.endsWith('/stops'))data=paged(catalogue.filter(s=>(u.searchParams.get('operators')??'').split(',').includes(s.operator_id)&&(!u.searchParams.get('q')||s.name.toLowerCase().includes(u.searchParams.get('q')!.toLowerCase()))));
  const coverage={status:'partial',message:'Previsões publicadas pelo operador.',actual_arrivals:false,actual_departures:false,history_collection_status:'collecting',source_updated_at:now};
  if(u.pathname.endsWith('/board')){if(failure)return r.fulfill({status:503,json:{message:'Fonte indisponível'}});const stop=decodeURIComponent(u.pathname.split('/').at(-2)!);requests.push(stop);data={stop_id:stop,revision:'fixture',directions:[{line_key:'cm:1',line_name:stop==='cm:A'?'Linha Alfa':'Linha Beta',direction_key:'destination',label:'Destino publicado',count:1}],coverage};}
  if(u.pathname.endsWith('/board/calls')){
   if(failure)return r.fulfill({status:503,json:{message:'Fonte indisponível'}});
   const stop=decodeURIComponent(u.pathname.split('/').at(-3)!);requests.push(stop);
   const prediction={at:new Date(Date.now()+expectedDelta).toISOString(),source_url:'https://api.carrismetropolitana.pt/v2',source_updated_at:now,collected_at:now,valid_until:new Date(Date.now()+30000).toISOString()};
   data={...paged([{id:stop,operator_id:'cm',stop_id:stop,stop_name:stop,stop_sequence:1,phase:'future',destination:'Destino publicado',arrival:{kind:'prediction',actual:null,prediction,schedule:null},departure:{kind:'unavailable',actual:null,prediction:null,schedule:null,reason:'Partida não publicada'}}]),coverage};
  }
  await r.fulfill({json:data});
 });return {requests,epoch,setFailure:(value:boolean)=>failure=value,setExpectedDelta:(value:number)=>expectedDelta=value};
}

test('station platforms do not become five nearby stations',async({page})=>{
 const f=await fixture(page,true);await page.goto('/');
 await page.getByLabel('Pesquisar carreira ou paragem').fill('Metro');
 await page.locator('.search-results button').first().click();
 const panel=page.locator('.detail-panel');await expect(panel.locator('.direction-matrix')).toBeVisible();
 await expect(panel.locator('.stop-navigation')).toHaveCount(0);
 await page.getByLabel('Pesquisar carreira ou paragem').fill('Metro');
 await expect(page.locator('.search-results button')).toHaveCount(1);
 expect(f.requests.every(id=>id==='metro:M')).toBe(true);
});

test('nearby navigation reconciles operator additions and removal',async({page})=>{
 const f=await fixture(page);await page.goto('/');
 await page.getByRole('button',{name:'Carris Metropolitana',exact:true}).click();
 await page.getByRole('button',{name:'Metro de Lisboa',exact:true}).click();
 await page.getByLabel('Pesquisar carreira ou paragem').fill('Alfa');await page.locator('.search-results button').last().click();
 const panel=page.locator('.detail-panel');await expect(panel).toContainText('1 de 2');
 await page.getByRole('button',{name:'Metro de Lisboa',exact:true}).click();
 await expect(panel).toContainText('1 de 3');
 await panel.getByRole('button',{name:'Paragem seguinte'}).click();
 await expect(panel.locator('.section-head h3')).toHaveText('Metro oculto');
 await page.getByRole('button',{name:'Metro de Lisboa',exact:true}).click();
 await expect(panel).toContainText('1 de 2');await expect(panel.locator('.section-head h3')).toHaveText('Paragem Alfa');
 await panel.getByRole('button',{name:'Paragem seguinte'}).click();
 await expect(panel.locator('.section-head h3')).toHaveText('Paragem Beta');
 const afterRemoval=f.requests.slice(f.requests.lastIndexOf('metro:M')+1);
 expect(afterRemoval.every(id=>id.startsWith('cm:'))).toBe(true);
});
for(const width of [1280,390])test(`nearby stop group and focused arrows at ${width}px`,async({page})=>{
 await page.setViewportSize({width,height:800});const f=await fixture(page);await page.goto('/');
 if(width<760)await page.getByRole('button',{name:'Abrir operadores'}).click();
 await page.getByRole('button',{name:'Carris Metropolitana',exact:true}).click();await page.getByRole('button',{name:'Metro de Lisboa',exact:true}).click();
 await page.getByLabel('Pesquisar carreira ou paragem').fill('Alfa');await page.locator('.search-results button').last().click();
 const panel=page.locator('.detail-panel');await expect(panel).toContainText('Linha Alfa');
 await expect(panel.getByRole('button',{name:'Paragem anterior'})).toBeDisabled();await expect(panel).toContainText('1 de 2');await expect(panel).toBeFocused();
 await page.keyboard.press('ArrowRight');await expect(panel).toContainText('Paragem Beta');await expect(panel).toContainText('Linha Beta');await expect(panel).not.toContainText('Linha Alfa');
 await expect(panel.getByRole('button',{name:'Paragem seguinte'})).toBeDisabled();await expect(panel).toContainText('2 de 2');
 await page.keyboard.press('ArrowRight');await expect(panel).toContainText('Paragem Beta');
 const canvas=await page.locator('.map canvas').boundingBox();await page.mouse.move(canvas!.x+canvas!.width-20,canvas!.y+280);await page.mouse.wheel(0,200);await page.mouse.down();await page.mouse.move(canvas!.x+canvas!.width-40,canvas!.y+300);await page.mouse.up();await expect(panel).toContainText('2 de 2');await expect(panel).toContainText('Paragem Beta');
 await panel.getByRole('button',{name:'Paragem anterior'}).click();await expect(panel).toContainText('Paragem Alfa');await expect(panel).toBeFocused();expect(f.requests.every(s=>s==='cm:A'||s==='cm:B')).toBe(true);
 await page.keyboard.press('Escape');await expect(panel).toHaveCount(0);await expect(page.getByLabel('Pesquisar carreira ou paragem')).toBeFocused();
});

test('map offers overlapping stops as explicit targets',async({page})=>{
 await fixture(page);await page.goto('/');await page.getByRole('button',{name:'Carris Metropolitana',exact:true}).click();await page.getByRole('button',{name:'Metro de Lisboa',exact:true}).click();
 const canvas=page.locator('.map canvas');await expect(canvas).toBeVisible();await page.waitForTimeout(600);const box=await canvas.boundingBox();await page.mouse.click(box!.x+box!.width/2,box!.y+box!.height/2);
 const panel=page.locator('.detail-panel');await expect(panel).toContainText('Escolher no mapa');await expect(panel.locator('.map-targets')).toContainText('Paragem Alfa');await expect(panel.locator('.map-targets')).toContainText('Paragem Beta');
 await panel.getByRole('button',{name:'Paragem Alfa Paragem · Carris Metropolitana'}).click();await expect(panel).toContainText('1 de 2');await expect(panel).not.toContainText('Paragem Gama');
 await panel.focus();await page.keyboard.press('Tab');await expect(panel.getByRole('button',{name:'Fechar detalhes'})).toBeFocused();
 await page.keyboard.press('Escape');await expect(panel).toHaveCount(0);await expect(canvas).toBeFocused();
});

test('a failed refresh never keeps a past ETA in upcoming passages',async({page})=>{
 const f=await fixture(page);f.setExpectedDelta(5000);await page.clock.install({time:new Date(f.epoch)});await page.goto('/');await page.getByRole('button',{name:'Carris Metropolitana',exact:true}).click();await page.getByRole('button',{name:'Metro de Lisboa',exact:true}).click();
 await page.getByLabel('Pesquisar carreira ou paragem').fill('Alfa');await page.locator('.search-results button').last().click();const panel=page.locator('.detail-panel');await expect(panel.locator('.direction-detail time')).toHaveCount(1);
 f.setFailure(true);await page.clock.fastForward(12000);await expect(panel.locator('.direction-detail time')).toHaveCount(0);await expect(panel).toContainText('Sem registo real');await expect(panel).toContainText('Fonte indisponível');
});

for(const width of [1280,390])test(`returning map drag keeps the stop group at ${width}px`,async({page})=>{
 await page.setViewportSize({width,height:800});await fixture(page);await page.goto('/');
 if(width<760)await page.getByRole('button',{name:'Abrir operadores'}).click();
 await page.getByRole('button',{name:'Carris Metropolitana',exact:true}).click();await page.getByRole('button',{name:'Metro de Lisboa',exact:true}).click();
 await page.getByLabel('Pesquisar carreira ou paragem').fill('Alfa');await page.locator('.search-results button').last().click();const panel=page.locator('.detail-panel');await expect(panel).toContainText('1 de 2');
 const box=(await page.locator('.map canvas').boundingBox())!,metrics=(await page.locator('.live-metrics').boundingBox())!,x=box.x+box.width-110,y=metrics.y+metrics.height+30;
 await expect.poll(async()=>page.evaluate(({x,y})=>!!document.elementFromPoint(x,y)?.closest('.map canvas'),{x,y})).toBe(true);
 await page.mouse.move(x,y);await page.mouse.down();await page.mouse.move(x-50,y+20,{steps:5});await page.mouse.move(x,y,{steps:5});await page.mouse.up();
 await expect(panel).toContainText('1 de 2');await expect(panel).toContainText('Paragem Alfa');
});

test('mobile touch pinch keeps the stop group',async({page})=>{
 await page.setViewportSize({width:390,height:800});await fixture(page);await page.goto('/');await page.getByRole('button',{name:'Abrir operadores'}).click();
 await page.getByRole('button',{name:'Carris Metropolitana',exact:true}).click();await page.getByRole('button',{name:'Metro de Lisboa',exact:true}).click();
 await page.getByLabel('Pesquisar carreira ou paragem').fill('Alfa');await page.locator('.search-results button').last().click();const panel=page.locator('.detail-panel');await expect(panel).toContainText('1 de 2');
 const metrics=(await page.locator('.live-metrics').boundingBox())!,y=metrics.y+metrics.height+30;
 await expect.poll(async()=>page.evaluate(y=>!!document.elementFromPoint(250,y)?.closest('.map canvas'),y)).toBe(true);
 const session=await page.context().newCDPSession(page);
 const touch=(type:string,points:{x:number,y:number,id:number}[])=>session.send('Input.dispatchTouchEvent',{type,touchPoints:points});
 await touch('touchStart',[{x:210,y,id:1},{x:290,y,id:2}]);
 await touch('touchMove',[{x:190,y,id:1},{x:310,y,id:2}]);
 await touch('touchMove',[{x:210,y,id:1},{x:290,y,id:2}]);
 await touch('touchEnd',[]);await session.detach();
 await expect(panel).toContainText('1 de 2');await expect(panel).toContainText('Paragem Alfa');
});
