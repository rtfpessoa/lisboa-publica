import {chromium,expect} from '/Users/rodrigo.fernandes/dev/lisboapublica/frontend/node_modules/@playwright/test/index.mjs';
import {writeFile} from 'node:fs/promises';
const browser=await chromium.launch({headless:true,args:['--enable-unsafe-swiftshader']});
const results=[];
try {
for(const width of [1280,390]) {
 const page=await browser.newPage({baseURL:'https://lisboapublica.rtfpessoa.xyz',viewport:{width,height:844}}),errors=[];page.setDefaultTimeout(20000);page.on('pageerror',e=>errors.push(e.message));console.log('Starting viewport',width);
 const now=new Date();const params=new URLSearchParams({operators:'cp',limit:'1',from:now.toISOString(),to:new Date(now.getTime()+86400000).toISOString()});const scheduleResponse=await page.request.get('/api/v1/trips?'+params);expect(scheduleResponse.status()).toBe(200);const scheduled=await scheduleResponse.json();expect(scheduled.data.length).toBe(1);const routeResponse=await page.request.get('/api/v1/routes/'+encodeURIComponent(scheduled.data[0].route_id));expect(routeResponse.status()).toBe(200);const cpRoute=await routeResponse.json();
 await page.goto('https://lisboapublica.rtfpessoa.xyz/');
 await expect(page.locator('.map canvas')).toBeVisible();
 const menu=async()=>{if(await page.getByRole('button',{name:'Abrir operadores',exact:true}).isVisible())await page.getByRole('button',{name:'Abrir operadores',exact:true}).click();};
 const outside=async()=>page.getByText('Veículos reportados',{exact:true}).click();
 await menu();await expect(page.getByRole('button',{name:'Metro de Lisboa',exact:true})).toHaveAttribute('aria-pressed','true');
 await expect(page.getByRole('button',{name:'CP',exact:true})).toHaveAttribute('aria-pressed','false');
 if(width<760)await page.getByRole('button',{name:'Fechar operadores'}).click();
 await page.getByRole('button',{name:'Camadas do mapa'}).click();
 for(const name of ['Linhas de metro','Percursos de autocarro','Linhas de comboio','Percursos fluviais'])await expect(page.getByRole('checkbox',{name})).toBeChecked();
 await page.getByRole('button',{name:'Fechar camadas'}).click();await expect(page.locator('.layer-panel')).toHaveCount(0);
 await page.getByRole('button',{name:'Camadas do mapa'}).click();await outside();await expect(page.locator('.layer-panel')).toHaveCount(0);
 await menu();await page.getByLabel('Pesquisar carreira ou paragem').fill('Roma');await expect(page.locator('.search-results button').first()).toBeVisible();
 await page.getByRole('button',{name:'Fechar resultados da pesquisa'}).click();await expect(page.locator('.search-popup')).toHaveCount(0);
 await page.getByLabel('Pesquisar carreira ou paragem').fill('Roma');await expect(page.locator('.search-results button').first()).toBeVisible();await page.locator('.search-results button').first().click();
 await expect(page.locator('.detail-panel')).toBeVisible();await page.getByRole('button',{name:'Fechar detalhes'}).click();await expect(page.locator('.detail-panel')).toHaveCount(0);
 await menu();await page.getByLabel('Pesquisar carreira ou paragem').fill('Roma');await page.locator('.search-results button').first().click();await expect(page.locator('.detail-panel')).toBeVisible();await outside();await expect(page.locator('.detail-panel')).toHaveCount(0);
 await menu();await page.getByRole('button',{name:'Metro de Lisboa',exact:true}).click();await page.getByRole('button',{name:'CP',exact:true}).click();
 await page.getByLabel('Pesquisar carreira ou paragem').fill(cpRoute.long_name);const result=page.locator('.search-results button').filter({hasText:cpRoute.long_name}).filter({has:page.getByText(cpRoute.short_name,{exact:true})}).first();await expect(result).toBeVisible();await result.click();console.log('Selected CP route',cpRoute.id,cpRoute.short_name);
 await page.getByRole('button',{name:'Viagens planeadas',exact:true}).click();await expect(page.locator('.detail-panel tbody tr').first()).toBeVisible({timeout:30000});
 await expect(page.getByRole('columnheader',{name:'Primeira paragem local (hora)'})).toBeVisible();await expect(page.locator('.detail-panel tbody')).toContainText('(planeado)');
 await page.keyboard.press('Escape');await expect(page.locator('.detail-panel')).toHaveCount(0);
 await menu();await page.getByRole('button',{name:'Remover filtro de carreira'}).click();await page.getByRole('button',{name:'CP',exact:true}).click();await page.locator('.main-operator').first().click();
 await page.getByRole('button',{name:'Frota',exact:true}).first().click();await page.getByRole('button',{name:'Veículos',exact:true}).click();
 await expect(page.locator('.page-panel tbody tr').first()).toBeVisible({timeout:30000});
 const plates=await page.locator('.page-panel tbody tr td:nth-child(2)').allTextContents();
 expect(plates.some(x=>/^[A-Z0-9]{2}-[A-Z0-9]{2}-[A-Z0-9]{2}$/.test(x))).toBe(true);
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
 await menu();await page.getByRole('button',{name:'Fontes e disponibilidade',exact:true}).click();await expect(page.getByRole('dialog')).toBeVisible();await page.keyboard.press('Escape');await expect(page.getByRole('dialog')).toHaveCount(0);
 await menu();await page.getByRole('button',{name:'Conta e chaves API',exact:true}).click();await expect(page.getByRole('dialog')).toBeVisible();await page.keyboard.press('Escape');await expect(page.getByRole('dialog')).toHaveCount(0);
 expect(errors).toEqual([]);await page.screenshot({path:`/tmp/lisboa-service-rollout/ui-${width}.png`});results.push({width,default_metro_and_overlays:true,popup_close_button_outside_escape:true,cp_full_scheduled_endpoints_and_local_times:true,formatted_bus_fleet_plates:plates.filter(x=>x!=='Indisponível').slice(0,3),horizontal_overflow:false,page_errors:errors});console.log('Passed viewport',width);await page.close();
}
await writeFile('/tmp/lisboa-service-rollout/browser.json',JSON.stringify(results,null,2)+'\n');console.log(JSON.stringify(results,null,2));
} finally {await browser.close();}
