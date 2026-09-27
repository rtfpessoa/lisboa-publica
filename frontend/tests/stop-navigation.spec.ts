import {test,expect,type Page} from '@playwright/test';
async function fixture(page:Page){
 const epoch=Date.now(),now=new Date(epoch).toISOString(),requests:string[]=[];let failure=false,expectedDelta=300000;
 const operators=['cm','metro'].map(id=>({id,name:id==='cm'?'Carris Metropolitana':'Metro de Lisboa',mode:id==='metro'?'metro':'bus',color:'#f5b800',status:'ok',static_status:'ok',live_updated_at:now,static_updated_at:now,reported_positions:0,estimated_positions:0,error:null,note:''}));
 const stops=[{id:'cm:A',name:'Paragem Alfa',lat:38.731,lon:-9.145},{id:'cm:B',name:'Paragem Beta',lat:38.73136,lon:-9.145},{id:'cm:C',name:'Paragem Gama',lat:38.73172,lon:-9.145},{id:'metro:M',name:'Metro oculto',lat:38.731,lon:-9.145}].map(s=>({...s,source_id:s.id,operator_id:s.id.split(':')[0],route_ids:[]}));
 const paged=(data:unknown[])=>({data,page:{limit:500,offset:0,total:data.length,has_more:false,revision:'fixture'}});
 await page.route('https://tiles.openfreemap.org/styles/positron',r=>r.fulfill({json:{version:8,sources:{},layers:[{id:'bg',type:'background',paint:{'background-color':'#fff'}}]}}));
 await page.route('**/api/v1/**',async r=>{
  const u=new URL(r.request().url());let data:unknown=paged([]);
  if(u.pathname.endsWith('/operators'))data=paged(operators);
  if(u.pathname.endsWith('/health'))data={status:'ok'};
  if(u.pathname.endsWith('/config'))data={dev_auth:false,live_refresh_seconds:5,history_retention_days:30,history_resolution_seconds:300};
  if(u.pathname.endsWith('/metrics'))data={speed_kmh:null};
  if(u.pathname.endsWith('/route-shapes'))data={...paged([]),coverage:[]};
  if(u.pathname.endsWith('/stops'))data=paged(stops.filter(s=>(u.searchParams.get('operators')??'').split(',').includes(s.operator_id)&&(!u.searchParams.get('q')||s.name.toLowerCase().includes(u.searchParams.get('q')!.toLowerCase()))));
  if(u.pathname.endsWith('/arrivals')){if(failure)return r.fulfill({status:503,json:{message:'Fonte indisponível'}});const stop=u.searchParams.get('stop_id')!;requests.push(stop);data={...paged([{id:stop,operator_id:'cm',stop_id:stop,route_id:'cm:1',route_name:stop==='cm:A'?'Linha Alfa':'Linha Beta',trip_id:'trip',headsign:'Destino publicado',kind:'prediction',scheduled_at:null,expected_at:new Date(Date.now()+expectedDelta).toISOString(),observed_at:null,source_url:'https://api.carrismetropolitana.pt/v2',valid_until:new Date(Date.now()+30000).toISOString()}]),availability:{status:'ok',planned_status:'ok',message:'Previsões publicadas pelo operador.',source_url:'https://api.carrismetropolitana.pt/v2'}}}
  await r.fulfill({json:data});
 });return {requests,epoch,setFailure:(value:boolean)=>failure=value,setExpectedDelta:(value:number)=>expectedDelta=value};
}
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
 await page.getByLabel('Pesquisar carreira ou paragem').fill('Alfa');await page.locator('.search-results button').last().click();const panel=page.locator('.detail-panel');await expect(panel.locator('.arrival')).toHaveCount(1);
 f.setFailure(true);await page.clock.fastForward(12000);await expect(panel.locator('.arrival')).toHaveCount(0);await expect(panel).toContainText('Sem próximas passagens publicadas nesta recolha.');
});

for(const width of [1280,390])test(`returning map drag keeps the stop group at ${width}px`,async({page})=>{
 await page.setViewportSize({width,height:800});await fixture(page);await page.goto('/');
 if(width<760)await page.getByRole('button',{name:'Abrir operadores'}).click();
 await page.getByRole('button',{name:'Carris Metropolitana',exact:true}).click();await page.getByRole('button',{name:'Metro de Lisboa',exact:true}).click();
 await page.getByLabel('Pesquisar carreira ou paragem').fill('Alfa');await page.locator('.search-results button').last().click();const panel=page.locator('.detail-panel');await expect(panel).toContainText('1 de 2');
 const box=(await page.locator('.map canvas').boundingBox())!,x=box.x+box.width-110,y=box.y+200;
 await page.mouse.move(x,y);await page.mouse.down();await page.mouse.move(x-50,y+20,{steps:5});await page.mouse.move(x,y,{steps:5});await page.mouse.up();
 await expect(panel).toContainText('1 de 2');await expect(panel).toContainText('Paragem Alfa');
});

test('mobile touch pinch keeps the stop group',async({page})=>{
 await page.setViewportSize({width:390,height:800});await fixture(page);await page.goto('/');await page.getByRole('button',{name:'Abrir operadores'}).click();
 await page.getByRole('button',{name:'Carris Metropolitana',exact:true}).click();await page.getByRole('button',{name:'Metro de Lisboa',exact:true}).click();
 await page.getByLabel('Pesquisar carreira ou paragem').fill('Alfa');await page.locator('.search-results button').last().click();const panel=page.locator('.detail-panel');await expect(panel).toContainText('1 de 2');
 const session=await page.context().newCDPSession(page);
 const touch=(type:string,points:{x:number,y:number,id:number}[])=>session.send('Input.dispatchTouchEvent',{type,touchPoints:points});
 await touch('touchStart',[{x:210,y:200,id:1},{x:290,y:200,id:2}]);
 await touch('touchMove',[{x:190,y:200,id:1},{x:310,y:200,id:2}]);
 await touch('touchMove',[{x:210,y:200,id:1},{x:290,y:200,id:2}]);
 await touch('touchEnd',[]);await session.detach();
 await expect(panel).toContainText('1 de 2');await expect(panel).toContainText('Paragem Alfa');
});
