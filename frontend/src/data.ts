import type {Page,Vehicle} from './api';
export const time=(s:string|null|undefined)=>s?new Date(s).toLocaleTimeString('pt-PT',{timeZone:'Europe/Lisbon',hour:'2-digit',minute:'2-digit'}):'—';
export const number=(v:number|null|undefined,digits=1)=>v==null?'—':v.toLocaleString('pt-PT',{maximumFractionDigits:digits});
export const unavailable=(v:string|null|undefined)=>v||'Indisponível';
// CP's official station spelling; presentation only, never an identity crosswalk.
// https://www.cp.pt/info/pt/gabinetes-de-apoio-ao-cliente/
export function passengerName(operator:string,value:string|null|undefined,fallback='Nome indisponível'){
 const name=value?.trim().replace(/\s+/g,' ');
 if(!name)return fallback;
 return operator==='cp'?name.replace(/\bSanta Apolonia\b/gi,'Santa Apolónia'):name;
}
const cpCategories:Record<string,string>={AP:'Alfa Pendular',IC:'Intercidades',R:'Regional',IR:'InterRegional'};
const metroNames:Record<string,string>={Az:'Azul',Am:'Amarela',Vd:'Verde',Vm:'Vermelha'};
export function routeName(operator:string,value:string|null|undefined){
 const name=passengerName(operator,value,'');
 if(operator==='metro')return metroNames[name]?`Linha ${metroNames[name]}`:['Azul','Amarela','Verde','Vermelha'].includes(name)?`Linha ${name}`:name;
 if(operator==='cp'){const parts=name.split(' · ');parts[0]=cpCategories[parts[0]]??parts[0];return parts.join(' · ')}
 return name;
}
export function observationAge(value:string,now:number){
 const stamp=Date.parse(value);if(!Number.isFinite(stamp)||stamp>now+30000)return 'hora não confirmada';
 const minutes=Math.floor(Math.max(0,now-stamp)/60000);
 return minutes===0?'agora':minutes<60?`há ${minutes} min`:`há ${Math.floor(minutes/60)} h`;
}
export function observationTime(value:string,now:number){return Number.isFinite(Date.parse(value))?`${time(value)} · ${observationAge(value,now)}`:'Hora não confirmada'}
export function sameVehicleService(a:Vehicle,b:Vehicle){return a.id===b.id&&a.operator_id===b.operator_id&&a.plan_id===b.plan_id&&a.trip_id===b.trip_id&&a.operational_date===b.operational_date&&(a.pattern_id??null)===(b.pattern_id??null)}
export const errorText=(e:unknown)=>{const v=e as {data?:{message?:string},message?:string};return v?.data?.message||v?.message||'Não foi possível carregar os dados.'};
export function today(){return new Intl.DateTimeFormat('en-CA',{timeZone:'Europe/Lisbon',year:'numeric',month:'2-digit',day:'2-digit'}).format(new Date())}
export function nextDay(date:string,delta=1){const d=new Date(date+'T12:00:00Z');d.setUTCDate(d.getUTCDate()+delta);return d.toISOString().slice(0,10)}
export function lisbonMidnight(date:string){const desired=Date.parse(date+'T00:00:00Z');let guess=desired;for(let i=0;i<3;i++){const parts=new Intl.DateTimeFormat('sv-SE',{timeZone:'Europe/Lisbon',year:'numeric',month:'2-digit',day:'2-digit',hour:'2-digit',minute:'2-digit',second:'2-digit',hourCycle:'h23'}).formatToParts(new Date(guess));const p=Object.fromEntries(parts.map(v=>[v.type,v.value]));const shown=Date.parse(`${p.year}-${p.month}-${p.day}T${p.hour}:${p.minute}:${p.second}Z`);guess+=desired-shown}return new Date(guess).toISOString()}
export async function allPages<T>(get:(args:{limit:number,offset:number,revision?:string})=>Promise<{data:T[],page:Page}>):Promise<T[]>{for(let attempt=0;attempt<3;attempt++){try{let offset=0,revision:string|undefined;const out:T[]=[];while(true){const page=await get({limit:500,offset,revision});out.push(...page.data);if(!page.page.has_more)return out;if(page.data.length===0||out.length>100000)throw new Error('Limite de coleção excedido.');revision=page.page.revision??undefined;offset+=page.page.limit}}catch(e){if((e as {status?:number}).status!==410||attempt===2)throw e}}return []}

export function plate(value:string|null|undefined,mode:string){
 if(!value?.trim())return 'Indisponível';
 const compact=value.trim().toUpperCase().replace(/[-\s]/g,'');
 return mode==='bus'&&/^(?:[A-Z]{2}[0-9]{4}|[0-9]{4}[A-Z]{2}|[0-9]{2}[A-Z]{2}[0-9]{2}|[A-Z]{2}[0-9]{2}[A-Z]{2})$/.test(compact)?compact.match(/.{2}/g)!.join('-'):value;
}
export function stopStatus(v:Vehicle){
 const station=v.stop_name?passengerName(v.operator_id,v.stop_name):undefined;
 const state=v.current_status==='STOPPED_AT'?`Parado${station?' em '+station:''}`:v.current_status==='INCOMING_AT'?`A chegar${station?' a '+station:' à paragem'}`:v.current_status==='IN_TRANSIT_TO'?'Entre paragens':undefined;
 if(!state)return v.last_known?'Posição anterior · estado de paragem não confirmado':'Estado de paragem não publicado';
 return `${v.last_known?'Último estado: ':''}${v.position_kind==='estimated'?'Estado estimado: ':''}${state}`;
}
