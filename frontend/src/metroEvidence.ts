import type {MetroTrain} from './api';

// Forecast context and modeled trip allocation are insufficient for a current link.
export function metroCurrentDirection(train:MetroTrain|undefined,now:number):boolean{
 return !!train&&train.association==='supported'&&Date.parse(train.valid_until)>now&&train.direction_evidence?.state==='confirmed';
}
