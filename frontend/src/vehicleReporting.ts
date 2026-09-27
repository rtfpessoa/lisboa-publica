import type {Vehicle} from './api';
import {positionIsOld} from './vehicleFreshness';

export function reportingLabel(vehicle:Vehicle):string {
 switch(vehicle.reporting?.state){
  case 'reporting':return 'A receber atualizações';
  case 'not_reporting':return 'Sem sinal atual';
  case 'unknown':return 'Sinal por confirmar';
  default:return '';
 }
}

export function reportingNotice(vehicle:Vehicle):string {
 switch(vehicle.reporting?.reason){
  case 'missing_from_snapshot':return 'O veículo deixou de aparecer na atualização da fonte aceite pela aplicação. Mostramos o último registo conhecido.';
  case 'observation_old':return 'A fonte deixou de fornecer uma observação recente deste veículo. Mostramos o último registo conhecido.';
  case 'source_error':return 'A fonte está indisponível; não podemos confirmar se o veículo continua a reportar.';
  case 'source_unverified':return 'A fonte ainda não foi verificada; o sinal do veículo está por confirmar.';
  case 'collection_old':return 'A aplicação não recebeu uma atualização recente da fonte; o sinal do veículo está por confirmar.';
  default:return '';
 }
}

// Source membership and elapsed observation age are independent warning signals.
export function reportingWarning(vehicle:Vehicle,now:number):boolean {
 return vehicle.reporting?.state==='not_reporting'||positionIsOld(vehicle,now);
}
