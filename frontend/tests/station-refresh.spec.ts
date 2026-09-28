import {test,expect,type Page} from '@playwright/test';

async function stationFixture(page:Page,validFor=60000,total=25){
 const epoch=Date.now(),iso=(n:number)=>new Date(epoch+n).toISOString();
 const stop={id:'metro:S',operator_id:'metro',source_id:'S',name:'Alameda',lat:38.731,lon:-9.145,route_ids:['red']};
 const directions=[{line_key:'red',line_name:'Linha Vermelha',color:'#e22',direction_key:'airport',label:'Aeroporto',count:null},{line_key:'red',line_name:'Linha Vermelha',color:'#e22',direction_key:'ss',label:'São Sebastião',count:null}];
 let revision=0,hold=false,failed=false,missing=false,expireOnce=false,ids=Array.from({length:total},(_,i)=>'row-'+i);
 let pending:(()=>Promise<void>)[]=[];const requests:URL[]=[];
 const coverage=(v:number)=>({status:'partial',message:'Previsões disponíveis; cobertura parcial. Quadro '+v,actual_arrivals:false,actual_departures:false,history_collection_status:'collecting',source_updated_at:iso(0)});
 const envelope=(data:unknown[])=>({data,page:{offset:0,total:data.length,limit:25,has_more:false,revision:'catalogue'}});
 const time={kind:'prediction',at:iso(600000),reason:'Sem registo real',actual:null,prediction:{at:iso(600000),source_url:'https://official.example',source_updated_at:iso(0),collected_at:iso(0),valid_until:iso(validFor)},schedule:null};
 await page.route('https://tiles.openfreemap.org/styles/positron',r=>r.fulfill({json:{version:8,sources:{},layers:[{id:'background',type:'background',paint:{'background-color':'#fff'}}]}}));
 await page.route('**/api/v1/**',async r=>{
  const u=new URL(r.request().url()),p=u.pathname;requests.push(u);let json:unknown=envelope([]);
  if(p.endsWith('/config'))json={dev_auth:false,live_refresh_seconds:5,history_retention_days:30,history_resolution_seconds:300};
  if(p.endsWith('/operators'))json=envelope([{id:'metro',name:'Metro de Lisboa',mode:'metro',color:'#e22',status:'ok',static_status:'ok',live_updated_at:iso(0),estimated_positions:1,reported_positions:1}]);
  if(p.endsWith('/route-shapes'))json={...envelope([]),coverage:[]};
  if(p.endsWith('/metro/status'))json={status:'unconfigured',message:'Fonte direta indisponível',lines:[]};
  if(p.endsWith('/stops'))json=envelope([stop]);
  if(p.endsWith('/board'))json={directions:missing?[directions[1]]:directions,coverage:coverage(++revision),revision:String(revision)};
  if(p.endsWith('/board/calls')){
   if(expireOnce){expireOnce=false;return r.fulfill({status:410,json:{message:'Changed revision'}})}
   if(failed)return r.fulfill({status:500,json:{message:'Unavailable'}});
   const version=Number(u.searchParams.get('revision')),selected=u.searchParams.get('direction_key'),snapshot=[...ids],offset=Number(u.searchParams.get('offset')??0);
   json={...envelope(snapshot.slice(offset,offset+25).map((id,n)=>({id:id+':'+selected,journey_id:null,line_key:'red',direction_key:selected,stop_id:stop.id,stop_name:stop.name,stop_sequence:n,destination:'',arrival:time,departure:{...time,kind:'unavailable',prediction:null},phase:'future',service_label:id}))),page:{offset,total:snapshot.length,limit:25,has_more:offset+25<snapshot.length,revision:String(version)},coverage:coverage(version)};
   const respond=async()=>{await r.fulfill({json})};
   if(hold){pending.push(respond);return}await respond();return;
  }
  await r.fulfill({json});
 });
 await page.goto('/');if((page.viewportSize()?.width??1280)<760)await page.getByRole('button',{name:'Abrir operadores'}).click();
 await page.getByLabel('Pesquisar carreira ou paragem').fill('alameda');await page.locator('.search-results button').last().click();
 await expect(page.locator('.direction-detail .transit-call:not(.call-head)')).toHaveCount(25);
 return {requests,hold:()=>{hold=true},release:async()=>{hold=false;const all=pending;pending=[];await Promise.all(all.map(fn=>fn()))},pending:()=>pending.length,setIds:(v:string[])=>{ids=v},fail:()=>{failed=true},recover:()=>{failed=false},removeDirection:()=>{missing=true},expire:()=>{expireOnce=true}};
}
for(const width of [1280,390])test(`complete station frame survives delayed revision and preserves reading at ${width}px`,async({page})=>{
 await page.setViewportSize({width,height:850});const f=await stationFixture(page),panel=page.locator('.detail-panel');
 const row=panel.locator('[data-call-id="row-10:airport"]');await row.locator('a').first().focus();await row.evaluate(el=>el.scrollIntoView({block:'start'}));
 const y=await row.evaluate(el=>el.getBoundingClientRect().top),scroll=await panel.evaluate(el=>el.scrollTop),coverage=await panel.locator('.notice').first().textContent();
 f.hold();await expect.poll(f.pending,{timeout:9000}).toBeGreaterThan(0);
 await expect(panel.locator('.direction-detail .transit-call:not(.call-head)')).toHaveCount(25);
 expect(await panel.locator('.notice').first().textContent()).toBe(coverage);expect(await panel.evaluate(el=>el.scrollTop)).toBe(scroll);
 await f.release();await expect.poll(async()=>panel.locator('.notice').first().textContent()).not.toBe(coverage);
 expect(Math.abs(await row.evaluate(el=>el.getBoundingClientRect().top)-y)).toBeLessThan(2);
 expect(await row.locator('a').first().evaluate(el=>el===document.activeElement)).toBe(true);
 await expect(panel.locator('.direction-matrix')).not.toContainText('Cobertura por confirmar');
});

test('same selected context survives errors, original prediction expiry and removed direction',async({page})=>{
 const f=await stationFixture(page,17000),panel=page.locator('.detail-panel');f.fail();
 await expect(panel).toContainText('Não foi possível atualizar',{timeout:12000});await expect(panel.locator('.direction-detail .transit-call:not(.call-head)')).toHaveCount(25);
 await expect(panel.locator('.direction-detail .call-time')).toHaveCount(0,{timeout:21000});
 await expect(panel.locator('.notice').first()).toContainText('Previsões anteriores expiradas');
 await expect(panel.locator('.notice').first()).not.toContainText('consulte os horários planeados');
 f.recover();f.removeDirection();await expect(panel).toContainText('Este sentido deixou de estar disponível',{timeout:12000});
 await expect(panel.locator('.direction-detail h4')).toContainText('Aeroporto');await expect(panel.getByRole('button',{name:'São Sebastião',exact:true})).toHaveAttribute('aria-pressed','false');
 await panel.getByRole('button',{name:'São Sebastião',exact:true}).click();await expect(panel.locator('.direction-detail h4')).toContainText('São Sebastião');
});

for(const width of [1280,390])test(`reading row and focus survive inserted, reordered and removed calls at ${width}px`,async({page})=>{
 await page.setViewportSize({width,height:850});const f=await stationFixture(page),panel=page.locator('.detail-panel');
 const row=(id:string)=>panel.locator(`[data-call-id="${id}:airport"]`);
 await row('row-10').locator('a').first().focus();await row('row-10').evaluate(el=>el.scrollIntoView({block:'start'}));
 const y=await row('row-10').evaluate(el=>el.getBoundingClientRect().top);
 f.setIds(['inserted',...Array.from({length:24},(_,i)=>'row-'+i)]);
 await expect(row('inserted')).toHaveCount(1,{timeout:12000});
 expect(Math.abs(await row('row-10').evaluate(el=>el.getBoundingClientRect().top)-y)).toBeLessThan(2);
 expect(await row('row-10').locator('a').first().evaluate(el=>el===document.activeElement)).toBe(true);
 f.setIds([...Array.from({length:25},(_,i)=>'row-'+i)].reverse());
 await expect(panel.locator('[data-call-id]').first()).toHaveAttribute('data-call-id','row-24:airport',{timeout:12000});
 expect(Math.abs(await row('row-10').evaluate(el=>el.getBoundingClientRect().top)-y)).toBeLessThan(2);
 const followingY=await row('row-9').evaluate(el=>el.getBoundingClientRect().top);
 f.setIds([...Array.from({length:25},(_,i)=>'row-'+i)].reverse().filter(id=>id!=='row-10'));
 await expect(row('row-10')).toHaveCount(0,{timeout:12000});
 expect(Math.abs(await row('row-9').evaluate(el=>el.getBoundingClientRect().top)-followingY)).toBeLessThan(2);
 expect(await row('row-9').locator('a').first().evaluate(el=>el===document.activeElement)).toBe(true);
});

test('an obsolete pending response cannot replace an explicit direction choice',async({page})=>{
 const f=await stationFixture(page),panel=page.locator('.detail-panel');f.hold();
 await expect.poll(f.pending,{timeout:12000}).toBe(1);
 await panel.getByRole('button',{name:'São Sebastião',exact:true}).click();
 await expect.poll(f.pending).toBe(2);await f.release();
 await expect(panel.locator('.direction-detail h4')).toContainText('São Sebastião');
 await expect(panel.locator('[data-call-id$=":ss"]')).toHaveCount(25);
 await expect(panel.locator('[data-call-id$=":airport"]')).toHaveCount(0);
});


test('a removed results page recovers the nearest available page in the same direction',async({page})=>{
 const f=await stationFixture(page,60000,26),panel=page.locator('.detail-panel');
 await panel.locator('.direction-detail .pagination').getByRole('button',{name:'Seguinte',exact:true}).click();
 await expect(panel.locator('[data-call-id]')).toHaveCount(1);
 await expect(panel.locator('[data-call-id]')).toHaveAttribute('data-call-id','row-25:airport');
 f.setIds(['row-0','row-1']);
 await expect(panel.locator('[data-call-id]')).toHaveCount(2,{timeout:12000});
 await expect(panel.locator('.direction-detail h4')).toContainText('Aeroporto');
 await expect(panel.locator('.direction-detail .pagination').getByRole('button',{name:'Anterior',exact:true})).toBeDisabled();
 await expect(panel.locator('.direction-detail .pagination')).toContainText('2 resultados');
});
