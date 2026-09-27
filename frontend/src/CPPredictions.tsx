import type {Arrival,CpPrediction,CpPredictionPage} from './api';
import {currentCPPredictions,predictionMatchesArrival} from './cp';
import {passengerName,number,time} from './data';

function Deviation({seconds}:{seconds:number|null}){
 return <span>Desvio previsto: {seconds==null?'indisponível':`${seconds>0?'+':''}${number(seconds/60,1)} min${seconds<0?' (adiantado)':''}`}</span>;
}

function Prediction({row,train=false}:{row:CpPrediction,train?:boolean}){
 return <div className="arrival cp-arrival" data-testid="cp-prediction">
  <strong>{train?passengerName('cp',row.stop_name,'Estação indisponível'):row.service_label?`Comboio ${row.service_label}`:passengerName('cp',row.route_name,'Linha indisponível')}</strong>
  <span>{train?passengerName('cp',row.destination_name,'Destino indisponível'):passengerName('cp',row.destination_name,'Destino indisponível')}</span>
  <time>{time(row.expected_at)}</time>
  <small>Previsão TML · CP{row.scheduled_at?` · planeado ${time(row.scheduled_at)}`:' · horário associado indisponível'}</small>
  <small><Deviation seconds={row.delay_seconds}/>{!row.service_date?' · dia de circulação não confirmado':''}</small>
  <small>Atualização da fonte às {time(row.source_updated_at)} · {passengerName('cp',row.route_name,'Linha indisponível')}</small>
 </div>;
}

export function CPStationArrivals({page,planned,now}:{page:CpPredictionPage,planned:Arrival[],now:number}){
 const predictions=currentCPPredictions(page.data,now);
 const schedule=planned.filter(a=>a.scheduled_at&&Date.parse(a.scheduled_at)>=now&&!predictions.some(p=>predictionMatchesArrival(p,a)));
 const rows=[...predictions.map(p=>({at:Date.parse(p.expected_at),id:p.id,p})),...schedule.map(a=>({at:Date.parse(a.scheduled_at!),id:a.id,a}))].sort((a,b)=>a.at-b.at||a.id.localeCompare(b.id));
 const expired=page.data.length>0&&!predictions.length;
 return <><p className="notice" role="status">{expired?'Previsões expiradas; mostramos os horários planeados.':page.availability.message}</p><h4>Próximas chegadas</h4>
  {rows.slice(0,20).map(row=>'p' in row?<Prediction key={row.id} row={row.p}/>:<div className="arrival" key={row.id}><strong>{passengerName(row.a.operator_id,row.a.route_name,'Linha indisponível')}</strong><span>{passengerName(row.a.operator_id,row.a.headsign,'Destino indisponível')}</span><time>{time(row.a.scheduled_at)}</time><small>Horário planeado</small></div>)}
  {!rows.length&&<p className="empty">Sem chegadas disponíveis neste intervalo.</p>}
  {rows.length>20&&<small>Mostramos as próximas 20 chegadas.</small>}
  <p className="subtle">Previsões de chegada à estação; não representam posições GPS nem pontualidade medida.</p>
 </>;
}

export function CPTrainCalls({rows,loading,message}:{rows:CpPrediction[],loading:boolean,message:string}){
 return <section className="cp-calls" aria-busy={loading}><h4>Próximas paragens previstas</h4>{loading&&<p role="status">A atualizar previsões CP…</p>}
  {rows[0]?.service_label&&<p><strong>Comboio {rows[0].service_label}</strong></p>}
  {rows.length>0&&<p className="notice" role="status">{message}</p>}
  {rows.slice(0,10).map(row=><Prediction key={row.id} row={row} train/>)}
  {!rows.length&&<p className="notice">{message} Previsões de chegada associadas a este serviço indisponíveis.</p>}
  <small>Fonte pública TML · CP. As previsões não alteram a posição reportada nem a hora da observação.</small>
 </section>;
}
