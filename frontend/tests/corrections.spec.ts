import {test,expect,type Page} from '@playwright/test';
async function fixtures(page:Page,emptyHistory=false,isolated=false){
 const now=new Date().toISOString(),bucket=new Date(Date.now()-600000).toISOString();
 await page.route('**/api/v1/**',async route=>{
  const path=new URL(route.request().url()).pathname;
  let data:unknown={data:[],page:{limit:500,offset:0,total:0,has_more:false,revision:'test'}};
  if(path.endsWith('/route-shapes'))data={data:[],coverage:[],page:{limit:500,offset:0,total:0,has_more:false,revision:'test'}};
  if(path.endsWith('/health'))data={status:'ok'};
  if(path.endsWith('/config'))data={dev_auth:false,history_retention_days:30,history_resolution_seconds:300,history_status:'collecting',storage_limit_bytes:5000000000};
  if(path.endsWith('/operators'))data={data:[{id:'carris',name:'Carris',color:'#f5b800',mode:'bus',status:'ok',static_status:'ok',reported_positions:1,estimated_positions:0,live_updated_at:now},{id:'cp',name:'CP',color:'#278044',mode:'train',status:'ok',static_status:'ok',reported_positions:0,estimated_positions:0,live_updated_at:now}],page:{limit:500,offset:0,total:2,has_more:false,revision:'test'}};
  if(path.endsWith('/vehicles'))data={data:[{id:'carris:1',operator_id:'carris',source_id:'1',position_kind:'reported',lat:38.73,lon:-9.15,observed_at:now,collected_at:now,speed_kmh:12,stale:false}],page:{limit:500,offset:0,total:1,has_more:false,revision:'test'}};
  if(path.endsWith('/metrics'))data={reported_vehicles:1,estimated_vehicles:0,speed_kmh:emptyHistory?null:12,distance_km:null,first_snapshot:null,unavailable_fields:[]};
  if(path.endsWith('/history'))data={data:emptyHistory?[]:[{bucket,reported_vehicles:1,estimated_vehicles:0,speed_kmh:12,distance_km:1}],page:{limit:500,offset:0,total:emptyHistory?0:1,has_more:false,revision:'test'}};
  if(path.endsWith('/history')&&isolated)data={data:[{bucket,reported_vehicles:1,estimated_vehicles:0,speed_kmh:12},{bucket:now,reported_vehicles:1,estimated_vehicles:0,speed_kmh:null},{bucket:new Date(Date.now()+60000).toISOString(),reported_vehicles:1,estimated_vehicles:0,speed_kmh:15}],page:{limit:500,offset:0,total:3,has_more:false,revision:'test'}};
  await route.fulfill({json:data});
 });
}
test('disconnected historical speed samples have visible markers',async({page})=>{
 await fixtures(page,false,true);await page.goto('/');await page.getByRole('button',{name:'Metro de Lisboa',exact:true}).click();await page.locator('.main-operator').first().click();await page.getByRole('button',{name:'Velocidade amostral',exact:true}).click();
 await expect(page.locator('.chart-modal .recharts-line-dot')).toHaveCount(2);
 await page.keyboard.press('Escape');await page.getByRole('button',{name:'Volume da frota',exact:true}).click();
 await expect(page.locator('.chart-modal .recharts-area-dot').first()).toBeVisible();
});
test('live speed is available before the first historical bucket closes',async({page})=>{
 await fixtures(page,true);await page.goto('/');await page.getByRole('button',{name:'Metro de Lisboa',exact:true}).click();await page.locator('.main-operator').first().click();await expect(page.locator('.live-metrics .metric').nth(1).locator('strong')).toHaveText('12');
 await page.getByRole('button',{name:'Volume da frota',exact:true}).click();await expect(page.locator('.chart-modal')).toContainText(/primeira amostra/i);
});
test('refresh retains values and changing operators cannot display previous values',async({page})=>{
 await page.clock.install();await fixtures(page);await page.goto('/');await page.getByRole('button',{name:'Metro de Lisboa',exact:true}).click();await page.locator('.main-operator').first().click();await expect(page.locator('.live-metrics .metric').first().locator('strong')).toHaveText('1');
 let release:()=>void=()=>{};const pending=new Promise<void>(r=>release=r);
 await page.route('**/api/v1/metrics?**',async r=>{await pending;await r.fulfill({json:{reported_vehicles:1,estimated_vehicles:0,speed_kmh:12}})});
 await page.clock.fastForward(31000);await expect(page.locator('.live-metrics .metric').first().locator('strong')).toHaveText('1');release();
 await page.route('**/api/v1/vehicles?**',async r=>{await r.fulfill({json:{data:[],page:{total:0,limit:500,offset:0,has_more:false,revision:'test'}}})});
 await page.locator('.main-operator').first().click();await page.getByRole('button',{name:'CP',exact:true}).click();
 await expect(page.locator('.live-metrics .metric').first().locator('strong')).toHaveText('0');
 await expect(page.getByRole('button',{name:'CP',exact:true})).toContainText('Sem observações atuais');
});
test('route overlays start enabled, follow operators and have independent controls',async({page})=>{
 await fixtures(page);const operators:string[]=[];
 await page.route('https://tiles.openfreemap.org/styles/positron',r=>r.fulfill({json:{version:8,sources:{},layers:[{id:'background',type:'background',paint:{'background-color':'#fff'}}]}}));
 await page.route('**/api/v1/route-shapes?**',async r=>{
  const selected=new URL(r.request().url()).searchParams.get('operators')!;operators.push(selected);
  const data=selected.split(',').map(id=>({id:id+':shape',operator_id:id,route_id:id+':1',shape_id:'1',direction_id:0,headsign:'Centro',color:'#f5b800',plan_id:'test',source_url:'https://example.test/official',updated_at:new Date().toISOString(),geometry:[[-9.16,38.72],[-9.15,38.73],[-9.14,38.73]]}));
  await r.fulfill({json:{data,coverage:selected.split(',').map(operator_id=>({operator_id,status:'available',updated_at:new Date().toISOString(),message:'GTFS'})),page:{limit:500,offset:0,total:data.length,has_more:false,revision:'test'}}});
 });
 await page.goto('/');await page.getByRole('button',{name:'Metro de Lisboa',exact:true}).click();await page.locator('.main-operator').first().click();await page.getByRole('button',{name:'Camadas do mapa'}).click();
 const metro=page.getByRole('checkbox',{name:'Linhas de metro'}),bus=page.getByRole('checkbox',{name:'Percursos de autocarro'});
 await expect(metro).toBeChecked();await expect(bus).toBeChecked();await bus.uncheck();await metro.uncheck();
 await expect(page.locator('.map')).toHaveAttribute('aria-busy','false');await expect(page.locator('.map canvas')).toBeVisible();await page.waitForTimeout(1000);const canvasShot=()=>page.screenshot({clip:{x:400,y:300,width:500,height:350}});const before=await canvasShot();
 await bus.check();await expect.poll(()=>operators.at(-1)).toBe('carris');await page.waitForTimeout(500);const after=await canvasShot();expect(after.equals(before)).toBe(false);await bus.uncheck();await page.waitForTimeout(500);expect((await canvasShot()).equals(before)).toBe(true);await bus.check();
 await metro.check();await expect(metro).toBeChecked();await expect(bus).toBeChecked();
 await page.getByRole('button',{name:'Metro de Lisboa',exact:true}).click();await expect(page.getByRole('button',{name:'Metro de Lisboa',exact:true})).toHaveAttribute('aria-pressed','true'); // Geometry may already be present from the initial selection in the query cache.
 await expect(page.locator('.layer-panel')).toHaveCount(0);await page.getByRole('button',{name:'Camadas do mapa'}).click();await bus.uncheck();await expect(bus).not.toBeChecked();await expect(metro).toBeChecked(); // Reuse Metro geometry already present in the query cache.
 await metro.uncheck();await expect(bus).not.toBeChecked();
});
